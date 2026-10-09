package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/atendimento"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
)

// Opcoes monta a bancada de avaliação.
type Opcoes struct {
	Provedor       llm.Provider
	Modelo         string
	Temperature    float64
	ThinkingBudget int
	SpecPath       string // eliza.openapi.json
	AgentesDir     string // agentes/*.yaml (usa eliza.yaml)
	Log            *slog.Logger
}

// Bancada junta o Eliza simulado, o Motor real e um SQLite temporário.
type Bancada struct {
	eliza *ElizaSim
	motor *agent.Motor
	ag    *agent.Agente
	st    *gravador
	dir   string
	n     atomic.Int64 // sufixo único de ticket e contato
}

// gravador guarda os turnos registrados (a interface Store não os lê de volta).
type gravador struct {
	store.Store
	mu     sync.Mutex
	turnos map[string][]store.Turno // por contato
}

func (g *gravador) RegistrarTurno(ctx context.Context, t store.Turno) error {
	g.mu.Lock()
	g.turnos[t.Contato] = append(g.turnos[t.Contato], t)
	g.mu.Unlock()
	return g.Store.RegistrarTurno(ctx, t)
}

func (g *gravador) ultimo(contato string) (store.Turno, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	l := g.turnos[contato]
	if len(l) == 0 {
		return store.Turno{}, 0
	}
	return l[len(l)-1], len(l)
}

// relogio avança 1 s por leitura a partir de qua 14/10/2026 10:00 local: o
// mundo é fixo, e a memória (ordem e janela de 24 h) continua coerente.
type relogio struct{ t atomic.Int64 }

func novoRelogio() *relogio {
	r := &relogio{}
	r.t.Store(time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC).UnixNano())
	return r
}

func (r *relogio) agora() time.Time {
	return time.Unix(0, r.t.Add(int64(time.Second))).UTC()
}

// Preparar sobe o simulado e monta o agente pela mesma Fabrica do main.
func Preparar(ctx context.Context, o Opcoes) (*Bancada, error) {
	spec, err := os.ReadFile(o.SpecPath)
	if err != nil {
		return nil, fmt.Errorf("spec do Eliza: %w", err)
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	b := &Bancada{eliza: NovoEliza(spec)}

	env := map[string]string{
		"ELIZA_BASE_URL":                  b.eliza.URL(),
		"ELIZA_ENCAMINHAMENTO_SECRET":     segredoEnc,
		"ELIZA_AUTOATENDIMENTO_API_TOKEN": tokenServico,
	}
	fab := &atendimento.Fabrica{
		Dir:     o.AgentesDir,
		Lookup:  func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Cliente: &http.Client{Timeout: 30 * time.Second},
		Timeout: 10 * time.Second,
		Modelo:  o.Modelo,
		Log:     log,
	}
	conj, err := fab.Montar(ctx)
	if err != nil {
		b.eliza.Fechar()
		return nil, err
	}
	b.ag = conj.Agentes["eliza"]
	if b.ag == nil {
		b.eliza.Fechar()
		return nil, errors.New("agentes/ não tem o agente eliza")
	}

	if b.dir, err = os.MkdirTemp("", "sola-agens-avaliacao-"); err != nil {
		b.eliza.Fechar()
		return nil, err
	}
	clock := novoRelogio()
	real, err := sqlite.Open(filepath.Join(b.dir, "agens.db"), clock.agora)
	if err != nil {
		b.Fechar()
		return nil, err
	}
	b.st = &gravador{Store: real, turnos: map[string][]store.Turno{}}
	b.motor = &agent.Motor{
		Store: b.st, LLM: o.Provedor, Log: log, Clock: clock.agora,
		Config: agent.Config{Temperature: o.Temperature, ThinkingBudget: o.ThinkingBudget, PausaHumanaH: 12},
	}
	return b, nil
}

// Fechar derruba o simulado e apaga o banco temporário.
func (b *Bancada) Fechar() {
	b.eliza.Fechar()
	if b.st != nil {
		b.st.Close()
	}
	_ = os.RemoveAll(b.dir)
}

// Resultado é o desfecho de um cenário (uma repetição).
type Resultado struct {
	Cenario     Cenario
	Rep         int
	Falhas      []string
	Ferramentas []string
	Respostas   []string // o que o cliente recebeu (só para -v)
	TokensIn    int
	TokensOut   int
	CustoMicro  int64
	Latencia    time.Duration // tempo real, somado das rodadas
}

func (r Resultado) Passou() bool { return len(r.Falhas) == 0 }

// Rodar executa o cenário: cada rodada vira um agent.Turno direto (sem HTTP de
// entrada nem debounce). Cada repetição usa uma conversa nova.
func (b *Bancada) Rodar(ctx context.Context, c Cenario, rep int) Resultado {
	res := Resultado{Cenario: c, Rep: rep}
	n := b.n.Add(1)
	ticket := fmt.Sprintf("tk-%d-%d", c.ID, n)
	contato := fmt.Sprintf("5511900%06d", n) // número fictício
	st := b.eliza.Novo(c, ticket)
	defer b.eliza.Esquecer(ticket)
	id := store.ConversaID{Agente: "eliza", Tenant: "avaliacao", Contato: contato}

	seq := 0
	for i, rd := range c.Rodadas {
		msgs := make([]mensagem.Mensagem, len(rd.Mensagens))
		for j, tx := range rd.Mensagens {
			seq++
			msgs[j] = mensagem.Mensagem{ID: fmt.Sprintf("in-%d-%d", n, seq), Texto: tx, Ticket: ticket,
				RecebidaEm: time.Date(2026, 10, 14, 13, 0, j, 0, time.UTC), NomeExibicao: c.Cliente.NomeExibicao}
		}
		nMsg, nEsc := b.eliza.Marca(st)
		_, antes := b.st.ultimo(contato)
		ini := time.Now()
		err := b.motor.Run(ctx, agent.Turno{Agente: b.ag, Conversa: id, Mensagens: msgs, Ticket: ticket,
			NomeExibicao: c.Cliente.NomeExibicao})
		res.Latencia += time.Since(ini)

		falha := func(f string, a ...any) {
			res.Falhas = append(res.Falhas, fmt.Sprintf("R%d: %s", i+1, fmt.Sprintf(f, a...)))
		}
		if err != nil {
			falha("Run: %v", err)
			break
		}
		turno, depois := b.st.ultimo(contato)
		if depois != antes+1 {
			falha("o turno não foi registrado")
			break
		}
		enviadas, escalas := b.eliza.Desde(st, nMsg, nEsc)
		res.TokensIn += turno.TokensEntrada
		res.TokensOut += turno.TokensSaida
		res.CustoMicro += turno.CustoMicroUSD
		res.Respostas = append(res.Respostas, fmt.Sprintf("R%d: %s", i+1, strings.Join(enviadas, " // ")))
		for _, f := range turno.Ferramentas {
			res.Ferramentas = append(res.Ferramentas, f.Nome)
		}
		for _, p := range verificar(rd.Esperar, turno, enviadas, escalas) {
			falha("%s", p)
		}
	}
	res.Falhas = append(res.Falhas, b.eliza.Conferir(st, c.EstadoFinal)...)
	return res
}

// verificar confere uma rodada: o turno registrado, o que o Eliza simulado
// recebeu (respostas e escalonamentos) e as strings esperadas.
func verificar(e Esperar, t store.Turno, enviadas []string, escalas []Escala) []string {
	var ruins []string
	chamadas := map[string]bool{}
	for _, f := range t.Ferramentas {
		chamadas[f.Nome] = true
	}
	for _, f := range e.FerramentasChamadas {
		if !chamadas[f] {
			ruins = append(ruins, "não chamou "+f)
		}
	}
	for _, f := range e.FerramentasProibidas {
		if chamadas[f] {
			ruins = append(ruins, "chamou "+f+" (proibida)")
		}
	}
	switch {
	case e.EscalouCom != "":
		achou := false
		for _, s := range escalas {
			achou = achou || s.Motivo == e.EscalouCom
		}
		if !achou {
			ruins = append(ruins, fmt.Sprintf("não escalou com %s (escalou: %s)", e.EscalouCom, motivos(escalas)))
		}
	case e.NaoEscalou && len(escalas) > 0:
		ruins = append(ruins, "escalou ("+motivos(escalas)+") e não devia")
	}
	if e.Fastpath != nil && (t.Modo == store.ModoFastpath) != *e.Fastpath {
		ruins = append(ruins, fmt.Sprintf("modo=%s, fastpath esperado %v", t.Modo, *e.Fastpath))
	}
	if e.Modo != "" && t.Modo != e.Modo {
		ruins = append(ruins, fmt.Sprintf("modo=%s, esperado %s", t.Modo, e.Modo))
	}
	if e.Resultado != "" && t.Resultado != e.Resultado {
		ruins = append(ruins, fmt.Sprintf("resultado=%s, esperado %s", t.Resultado, e.Resultado))
	}
	resposta := strings.Join(enviadas, "\n")
	for _, s := range e.RespostaContem {
		if !contemAlgum(resposta, s) {
			ruins = append(ruins, fmt.Sprintf("resposta sem %q", s))
		}
	}
	for _, s := range e.RespostaNaoContem {
		if contemAlgum(resposta, s) {
			ruins = append(ruins, fmt.Sprintf("resposta com %q", s))
		}
	}
	return ruins
}

func motivos(l []Escala) string {
	if len(l) == 0 {
		return "nada"
	}
	var m []string
	for _, e := range l {
		m = append(m, e.Motivo)
	}
	return strings.Join(m, ",")
}
