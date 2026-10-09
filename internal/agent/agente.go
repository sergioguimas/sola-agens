package agent

import (
	"log/slog"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/openapi"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
	"sola-agens/internal/transcricao"
)

// Agente é o agente em tempo de execução: o registro (yaml) mais o que o boot
// montou a partir da OpenAPI. Imutável depois de criado; a recarga (SIGHUP)
// cria outro e troca o ponteiro, e o turno em andamento termina com o antigo.
type Agente struct {
	Registro    agentes.Agente
	Ferramentas []openapi.Ferramenta
	Exec        conector.Executor
	Sistema     sistema.Sistema
	Modelo      string // já resolvido: registro.modelo ou AGENS_MODEL

	porNome map[string]openapi.Ferramenta
}

// NovoAgente monta o runtime e o índice de ferramentas por nome.
func NovoAgente(reg agentes.Agente, ferramentas []openapi.Ferramenta, exec conector.Executor, sist sistema.Sistema, modelo string) *Agente {
	a := &Agente{Registro: reg, Ferramentas: ferramentas, Exec: exec, Sistema: sist, Modelo: modelo,
		porNome: make(map[string]openapi.Ferramenta, len(ferramentas))}
	for _, f := range ferramentas {
		a.porNome[f.Decl.Name] = f
	}
	return a
}

// Ferramenta devolve a ferramenta pelo nome (operationId).
func (a *Agente) Ferramenta(nome string) (openapi.Ferramenta, bool) {
	f, ok := a.porNome[nome]
	return f, ok
}

// Turno é um item da fila: tudo que o agente precisa para processar uma
// rajada de mensagens de uma conversa.
type Turno struct {
	Agente       *Agente
	Conversa     store.ConversaID
	Mensagens    []mensagem.Mensagem // em ordem de RecebidaEm
	Ticket       string              // o mais recente da rajada
	NomeExibicao string              // o mais recente
}

// Config são os parâmetros do motor que vêm do ambiente (specs/05 §7).
type Config struct {
	Temperature    float64
	ThinkingBudget int
	PausaHumanaH   int // fallback de AGENS_PAUSA_HUMANA_HORAS
}

// Motor executa turnos. Só depende de interfaces (specs/05 §3).
type Motor struct {
	Store  store.Store
	LLM    llm.Provider
	Config Config
	Clock  func() time.Time
	Log    *slog.Logger
	// Transcritor converte o áudio do cliente em texto (F5). nil = o motor não
	// processa áudio: a política "processar" vale como resposta_fixa.
	Transcritor transcricao.Transcritor
}

func (m *Motor) agora() time.Time {
	if m.Clock != nil {
		return m.Clock()
	}
	return time.Now()
}

func (m *Motor) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// padrao é o dicionário do motor com o fallback de pausa do ambiente.
func (m *Motor) padrao() Dicionario {
	d := Padrao()
	d.PausaHumanaHoras = m.Config.PausaHumanaH
	if d.PausaHumanaHoras <= 0 {
		d.PausaHumanaHoras = 12
	}
	return d
}
