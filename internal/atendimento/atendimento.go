// Package atendimento liga as peças do motor: o conjunto de agentes em tempo
// de execução (trocável por SIGHUP), o debounce, a fila por conversa e o
// agente. O Servico implementa httpapi.Handler (specs/05 §3, specs/03 F1/F6/F8).
package atendimento

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
	"sola-agens/internal/debounce"
	"sola-agens/internal/entrada"
	"sola-agens/internal/httpapi"
	"sola-agens/internal/openapi"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
	"sola-agens/internal/trava"
	"sola-agens/internal/worker"
)

// Conjunto é o que o boot (e cada recarga) monta: troca atômica de uma vez só.
// Turno em andamento termina com o conjunto que já tinha em mãos.
type Conjunto struct {
	Agentes  map[string]*agent.Agente
	Registro []agentes.Agente // na ordem dos arquivos, para o httpapi
}

// Fabrica monta um Conjunto a partir de agentes/*.yaml e das OpenAPIs.
// TimeoutMidia é o mínimo para baixar mídia (o Eliza espera até 15 s pela Evolution).
const TimeoutMidia = 30 * time.Second

type Fabrica struct {
	Dir     string
	Lookup  func(string) (string, bool) // os.LookupEnv
	Cliente *http.Client                // download da spec
	Timeout time.Duration               // AGENS_SISTEMA_TIMEOUT, por chamada do conector
	Modelo  string                      // AGENS_MODEL, quando o yaml não define
	Log     *slog.Logger
	// Baixar é a fonte da spec; nil = openapi.Carregar. Existe para os testes.
	Baixar func(ctx context.Context, cli *http.Client, ag agentes.Agente) ([]byte, error)
}

// Montar lê o registro, baixa e converte cada OpenAPI e liga conector e
// sistema. Qualquer falha devolve erro e nada é trocado (boot: o processo sai;
// SIGHUP: mantém o conjunto anterior).
func (f *Fabrica) Montar(ctx context.Context) (*Conjunto, error) {
	regs, err := agentes.Carregar(f.Dir, f.Lookup)
	if err != nil {
		return nil, err
	}
	if len(regs) == 0 {
		return nil, errors.New("nenhum agente em " + f.Dir)
	}
	baixar := f.Baixar
	if baixar == nil {
		baixar = openapi.Carregar
	}
	c := &Conjunto{Agentes: make(map[string]*agent.Agente, len(regs)), Registro: regs}
	for _, reg := range regs {
		spec, err := baixar(ctx, f.Cliente, reg)
		if err != nil {
			return nil, fmt.Errorf("agente %s: %w", reg.ID, err)
		}
		tools, err := openapi.Converter(spec, reg)
		if err != nil {
			return nil, fmt.Errorf("agente %s: %w", reg.ID, err)
		}
		// Typo no yaml: o fastpath ficaria desligado em silêncio.
		for campo, op := range map[string]string{"confirmar": reg.Operacoes.Confirmar, "cancelar": reg.Operacoes.Cancelar} {
			if op != "" && !temFerramenta(tools, op) {
				return nil, fmt.Errorf("agente %s: operacoes.%s=%q não é uma ferramenta (x-atendente) da spec", reg.ID, campo, op)
			}
		}
		exec := conector.NovoHTTP(conector.ConfigDe(&reg), f.Timeout)
		sist, err := sistema.Novo(exec, spec, reg.Operacoes)
		if err != nil {
			return nil, fmt.Errorf("agente %s: %w", reg.ID, err)
		}
		sist.ComMidia(conector.NovoHTTP(conector.ConfigDe(&reg), max(f.Timeout, TimeoutMidia)))
		modelo := reg.Modelo
		if modelo == "" {
			modelo = f.Modelo
		}
		c.Agentes[reg.ID] = agent.NovoAgente(reg, tools, exec, sist, modelo)
		if f.Log != nil {
			f.Log.Info("ferramentas carregadas", "agente", reg.ID, "ferramentas", len(tools))
		}
	}
	return c, nil
}

func temFerramenta(l []openapi.Ferramenta, nome string) bool {
	for _, f := range l {
		if f.Decl.Name == nome {
			return true
		}
	}
	return false
}

// Servico é o handler do httpapi: Mensagem → debounce; DeMim → descarrega a
// rajada daquela conversa e entra na mesma fila.
type Servico struct {
	Deb  *debounce.Debounce
	Pool *worker.Pool

	atual   atomic.Pointer[Conjunto]
	fabrica *Fabrica
	log     *slog.Logger
	// AoTrocar é chamado depois de cada troca bem-sucedida (o main atualiza o httpapi).
	AoTrocar func([]agentes.Agente)
}

var _ httpapi.Handler = (*Servico)(nil)

// Opcoes de Novo.
type Opcoes struct {
	Conjunto     *Conjunto
	Fabrica      *Fabrica // opcional; sem ela, Recarregar devolve erro
	Runner       worker.Runner
	Trava        trava.Trava
	MaxConversas int
	Relogio      debounce.Relogio // nil = tempo real
	Log          *slog.Logger
}

// Novo monta debounce e pool em torno do conjunto inicial.
func Novo(o Opcoes) *Servico {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	s := &Servico{fabrica: o.Fabrica, log: log}
	s.atual.Store(o.Conjunto)
	s.Pool = worker.New(o.Runner, o.Trava, log)
	s.Deb = debounce.New(s.entregar, o.MaxConversas, o.Relogio)
	return s
}

// Atual devolve o conjunto em uso.
func (s *Servico) Atual() *Conjunto { return s.atual.Load() }

func (s *Servico) agente(id string) (*agent.Agente, error) {
	if ag := s.atual.Load().Agentes[id]; ag != nil {
		return ag, nil
	}
	return nil, fmt.Errorf("agente %q sem runtime", id)
}

// traduz o erro do pool para o que o httpapi entende (503).
func traduz(err error) error {
	if errors.Is(err, worker.ErrFilaCheia) {
		return httpapi.ErrFilaCheia
	}
	return err
}

// entregar leva o lote fechado do debounce para a fila. Roda com o lock do
// debounce: só enfileira, nunca bloqueia.
func (s *Servico) entregar(l debounce.Lote) error {
	ag, err := s.agente(l.Conversa.Agente)
	if err != nil {
		s.log.Error("lote sem agente", "agente", l.Conversa.Agente, "erro", err.Error())
		return err
	}
	err = s.Pool.EnfileirarTurno(agent.Turno{Agente: ag, Conversa: l.Conversa, Mensagens: l.Mensagens,
		Ticket: l.Ticket, NomeExibicao: l.NomeExibicao})
	if err != nil {
		// Depois de a requisição já ter respondido 202 não há como avisar o sistema.
		s.log.Error("lote descartado: fila cheia", "agente", l.Conversa.Agente, "tenant", l.Conversa.Tenant,
			"mensagens", len(l.Mensagens))
	}
	return traduz(err)
}

func conversaDe(ag agentes.Agente, e entrada.Entrada) store.ConversaID {
	return store.ConversaID{Agente: ag.ID, Tenant: e.Tenant, Contato: e.Contato}
}

// Mensagem entrega a mensagem do cliente ao debounce (F1 passo 9).
func (s *Servico) Mensagem(_ context.Context, ag agentes.Agente, e entrada.Entrada) error {
	rt, err := s.agente(ag.ID)
	if err != nil {
		return err
	}
	lim := rt.Registro.Limites
	msg := e.Mensagem
	msg.Ticket = e.Ticket // cada mensagem guarda o próprio ticket (baixarMidia, F5)
	return traduz(s.Deb.Adicionar(conversaDe(ag, e),
		time.Duration(lim.RajadaMs)*time.Millisecond, time.Duration(lim.RajadaMaxMs)*time.Millisecond,
		msg, e.Ticket, e.NomeExibicao))
}

// DeMim descarrega a rajada aberta da conversa e só então entra na fila, para
// manter a ordem (F8); a fila é a mesma do turno (F3).
func (s *Servico) DeMim(_ context.Context, ag agentes.Agente, e entrada.Entrada) error {
	rt, err := s.agente(ag.ID)
	if err != nil {
		return err
	}
	c := conversaDe(ag, e)
	s.Deb.Flush(c)
	return traduz(s.Pool.EnfileirarDeMim(rt, c, e.Mensagem))
}

// Recarregar monta um conjunto novo e troca (SIGHUP, F6). Inválido: mantém o
// anterior, loga e devolve o erro.
func (s *Servico) Recarregar(ctx context.Context) error {
	if s.fabrica == nil {
		return errors.New("recarga sem fábrica")
	}
	novo, err := s.fabrica.Montar(ctx)
	if err != nil {
		s.log.Error("recarga inválida: mantendo o conjunto anterior", "erro", err.Error())
		return err
	}
	s.atual.Store(novo)
	if s.AoTrocar != nil {
		s.AoTrocar(novo.Registro)
	}
	s.log.Info("agentes recarregados", "agentes", len(novo.Agentes))
	return nil
}

// Close descarrega o debounce e espera a fila (F6): o que estava acumulado vira
// turno antes de o pool fechar.
func (s *Servico) Close(espera time.Duration) error {
	s.Deb.Close()
	return s.Pool.Close(espera)
}
