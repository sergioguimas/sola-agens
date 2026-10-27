// Package store define a interface de persistência do agens e seus tipos
// (specs/01 §11). A implementação está em store/sqlite; a bateria de
// contrato, em store/storetest.
package store

import (
	"context"
	"errors"
	"time"

	"sola-agens/internal/mensagem"
)

// Papéis de uma mensagem na memória.
const (
	PapelCliente = "cliente"
	PapelAgente  = "agente"
	PapelHumano  = "humano"
	// PapelAviso: envio automático do sistema (lembrete, notificação), só contexto (A11).
	PapelAviso = "aviso"
)

// Modos de um turno (specs/01 §8).
const (
	ModoLLM      = "llm"
	ModoFastpath = "fastpath"
	ModoFixa     = "fixa"
	ModoPausado  = "pausado"
	ModoErro     = "erro"
)

// Erros sentinela.
var (
	ErrConversaInvalida = errors.New("conversa sem agente, tenant ou contato")
	ErrPapelInvalido    = errors.New("papel de mensagem inválido")
	ErrModoInvalido     = errors.New("modo de turno inválido")
)

// ConversaID é a chave da conversa: agente + tenant + contato (specs/01 §1).
type ConversaID struct {
	Agente  string
	Tenant  string
	Contato string
}

// Valida garante que as três chaves existem; nenhuma operação aceita chave vazia.
func (c ConversaID) Valida() error {
	if c.Agente == "" || c.Tenant == "" || c.Contato == "" {
		return ErrConversaInvalida
	}
	return nil
}

// Chave é "agente:tenant:contato": a chave da fila, do debounce e da trava
// (specs/05 §4). Só para uso interno; nunca vai para log (tem o contato).
func (c ConversaID) Chave() string { return c.Agente + ":" + c.Tenant + ":" + c.Contato }

// Conversa é o estado da conversa: ativa ou pausada até um instante.
type Conversa struct {
	ID           ConversaID
	PausadaAte   *time.Time // nil = ativa
	ConfigJSON   []byte     // última config combinada recebida do sistema; nil se nunca
	ConfigEm     *time.Time
	CriadaEm     time.Time
	AtualizadaEm time.Time
}

// MensagemMemoria é uma mensagem da janela de memória (specs/01 §7).
type MensagemMemoria struct {
	Papel    string  // cliente | agente | humano
	Conteudo *string // nil depois da retenção
	Anexos   []mensagem.Anexo
	CriadaEm time.Time // zero = agora (clock do Store)
}

// FerramentaLog é uma chamada de ferramenta no log do turno (sem argumentos).
type FerramentaLog struct {
	Nome   string `json:"nome"`
	Status int    `json:"status"`
	Codigo string `json:"codigo"`
}

// Turno é a linha de auditoria e custo de um turno (specs/01 §8). Sem texto.
type Turno struct {
	ConversaID
	Modo          string
	MensagemIDs   []string
	Modelo        string
	Iteracoes     int
	Ferramentas   []FerramentaLog
	TokensEntrada int
	TokensSaida   int
	CustoMicroUSD int64
	LatenciaMS    int64
	Resultado     string // ok | erro_llm | erro_sistema | erro_envio | ticket_invalido
	Escalou       bool
	CriadoEm      time.Time // zero = agora (clock do Store)
}

// Store é toda a persistência do agens. Nenhuma leitura atravessa tenant.
type Store interface {
	// Eventos e ecos.
	MarcarEvento(ctx context.Context, agente, mensagemID string) (novo bool, err error)
	DesmarcarEvento(ctx context.Context, agente, mensagemID string) error
	RegistrarEco(ctx context.Context, agente, mensagemID string) error
	EhEco(ctx context.Context, agente, mensagemID string) (bool, error)

	// Conversa.
	Conversa(ctx context.Context, id ConversaID) (Conversa, error) // cria vazia se não existe
	Pausar(ctx context.Context, id ConversaID, ate time.Time) error
	GuardarConfig(ctx context.Context, id ConversaID, configJSON []byte) error

	// Memória.
	AnexarMensagem(ctx context.Context, id ConversaID, m MensagemMemoria) error
	// Janela devolve, em ordem cronológica crescente, as últimas max
	// mensagens com CriadaEm >= desde.
	Janela(ctx context.Context, id ConversaID, max int, desde time.Time) ([]MensagemMemoria, error)

	// Log.
	RegistrarTurno(ctx context.Context, t Turno) error

	// Manutenção (specs/01 §9).
	ExpurgarTexto(ctx context.Context, antesDe time.Time) (int64, error)
	ApagarEventosEcos(ctx context.Context, antesDe time.Time) (int64, error)
	ApagarContato(ctx context.Context, id ConversaID) error
	JobFeito(ctx context.Context, nome, dataRef string) (bool, error)
	MarcarJob(ctx context.Context, nome, dataRef string) error

	Close() error
}
