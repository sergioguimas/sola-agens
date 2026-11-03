package store

import (
	"context"
	"errors"
	"time"
)

// Estados de uma sessão de treino (specs/06 §6.1).
const (
	SessaoAberta       = "aberta"
	SessaoEncerrada    = "encerrada"
	SessaoExtraida     = "extraida"
	SessaoErroExtracao = "erro_extracao"
)

// Papéis e tipos de uma mensagem de treino (specs/06 §10).
const (
	PapelOperador = "operador"

	TipoLivre          = "livre"
	TipoEnsaio         = "ensaio"
	TipoRespostaEnsaio = "resposta_ensaio"
)

// Erros sentinela do treino.
var (
	// ErrNaoEncontrado: não existe ou é de outro tenant; a API não distingue
	// os dois, para não confirmar que o id existe (specs/06 §4).
	ErrNaoEncontrado   = errors.New("não encontrado")
	ErrLimiteSessoes   = errors.New("limite de sessões do dia")
	ErrSessaoEncerrada = errors.New("sessão encerrada")
)

// SessaoTreino é uma conversa de treino de um operador.
type SessaoTreino struct {
	ID          string
	Agente      string
	Tenant      string
	OperadorID  string
	Estado      string
	RoteiroJSON []byte
	AbertaEm    time.Time
	UltimaMsgEm time.Time
	EncerradaEm *time.Time
}

// SessaoNova é o pedido de abertura. O limite do dia conta as sessões abertas
// a partir de InicioDia e não vale para quem já tem uma aberta (specs/06 S18).
type SessaoNova struct {
	Agente      string
	Tenant      string
	OperadorID  string
	RoteiroJSON []byte
	InicioDia   time.Time
	LimiteDia   int
}

// MensagemTreino é uma fala da sessão. Conteudo fica nil depois da retenção.
type MensagemTreino struct {
	Papel    string
	Tipo     string
	Cenario  string
	Conteudo *string
	CriadaEm time.Time // zero = agora (clock do Store)
}

// StoreTreino é a persistência do chat de treino. Fica separada de Store
// porque só o modo de treino a usa. A fase de aprendizado acrescenta
// observações, perfis e notas (specs/06 §10.1).
type StoreTreino interface {
	// AbrirSessao devolve a sessão aberta do operador ou cria uma, numa só
	// transação. ErrLimiteSessoes se precisaria criar e o dia já lotou.
	AbrirSessao(ctx context.Context, n SessaoNova) (s SessaoTreino, nova bool, err error)
	// SessaoTreino lê a sessão; ErrNaoEncontrado se for de outro agente ou tenant.
	SessaoTreino(ctx context.Context, agente, tenant, id string) (SessaoTreino, error)
	// MensagensTreino devolve as falas em ordem cronológica.
	MensagensTreino(ctx context.Context, sessaoID string) ([]MensagemTreino, error)
	// GravarTurnoTreino grava as falas do turno e o roteiro numa transação;
	// ErrSessaoEncerrada se a sessão não está mais aberta.
	GravarTurnoTreino(ctx context.Context, sessaoID string, msgs []MensagemTreino, roteiroJSON []byte) error
	// EncerrarSessao passa de aberta para encerrada; em outro estado não faz nada.
	EncerrarSessao(ctx context.Context, id string, em time.Time) error
}
