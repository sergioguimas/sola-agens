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
	// ErrJaExtraida: a sessão já passou pela extração; aplicar de novo contaria
	// a evidência duas vezes.
	ErrJaExtraida = errors.New("sessão já extraída")
)

// Origem e estado de uma observação (specs/06 §10).
const (
	OrigemExtraida  = "extraida"
	OrigemDeclarada = "declarada"
	OrigemMetrica   = "metrica"

	ObservacaoCandidata = "candidata"
	ObservacaoRejeitada = "rejeitada"

	NotaPendente   = "pendente"
	NotaAplicada   = "aplicada"
	NotaDescartada = "descartada"
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
	// VersaoGerada é a versão de perfil que a sessão criou; nil se nenhuma.
	VersaoGerada *int
	// Tentativas de extração já feitas (specs/06 §11 T-F4).
	Tentativas int
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

// ObservacaoNova é um padrão visto na sessão, antes da fusão.
type ObservacaoNova struct {
	Campo         string
	Valor         string // normalizado: é a chave da fusão
	ValorExibicao string
	Cenario       string // só em exemplos
	Origem        string
	Citacao       string
	Ocorrencias   int
}

// Observacao é um padrão com a evidência acumulada entre sessões.
type Observacao struct {
	ID            string
	Campo         string
	Valor         string
	ValorExibicao string
	Cenario       string
	Origem        string
	Estado        string
	Ocorrencias   int
	Sessoes       int
	UltimaSessao  string
	Citacao       string
}

// NotaNova é uma afirmação de fato captada no treino (T5, S12).
type NotaNova struct {
	ID      string
	Texto   string
	Citacao string
}

// Nota é uma afirmação de fato, com o estado da decisão do operador.
type Nota struct {
	ID       string
	Texto    string
	Citacao  string
	Estado   string
	CriadaEm time.Time
}

// VersaoPerfil é uma linha do histórico do perfil.
type VersaoPerfil struct {
	Versao   int
	Ativa    bool
	Origem   string
	DiffJSON []byte
	CriadaEm time.Time
}

// Derivacao é o perfil que sai das observações.
type Derivacao struct {
	PerfilJSON []byte
	DiffJSON   []byte
	Mudou      bool
	// Rejeitar e Restaurar acompanham a volta de versão (S13, S34): o que saiu com
	// ela e o que voltou com ela. Só valem em EditarPerfil.
	Rejeitar  []string
	Restaurar []string
}

// Edicao é uma mudança feita pelo operador no painel. Roda numa transação:
// muda os estados, deriva e grava a versão, se o perfil mudou (specs/06 S33).
type Edicao struct {
	Agente         string
	Tenant         string
	OperadorID     string
	Origem         string // edicao | volta:{n} | zerado
	Rejeitar       []string
	Restaurar      []string
	RejeitarTodas  bool
	DescartarNotas bool
	Em             time.Time // instante da resolução das notas
	// Derivar recebe as observações já com os estados novos e o perfil ativo.
	Derivar func(obs []Observacao, ativoJSON []byte) (Derivacao, error)
}

// Extracao é o resultado de uma sessão, aplicado de uma vez (specs/06 S26).
type Extracao struct {
	Agente      string
	Tenant      string
	SessaoID    string
	OperadorID  string
	Observacoes []ObservacaoNova
	Notas       []NotaNova
	// Derivar roda dentro da transação, com as observações já fundidas e o JSON
	// do perfil ativo (nil se não há). Não pode tocar a base.
	Derivar func(obs []Observacao, ativoJSON []byte) (Derivacao, error)
}

// ResultadoExtracao diz o que a aplicação produziu.
type ResultadoExtracao struct {
	Versao      *int
	Observacoes []Observacao // do tenant, depois da fusão
}

// StoreTreino é a persistência do chat de treino. Fica separada de Store
// porque só o modo de treino a usa (specs/06 §10.1).
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

	// SessoesInativas lista as abertas sem mensagem desde antesDe.
	SessoesInativas(ctx context.Context, agente string, antesDe time.Time) ([]SessaoTreino, error)
	// SessoesPendentes lista as que faltam extrair, com menos de maxTentativas.
	SessoesPendentes(ctx context.Context, agente string, maxTentativas int) ([]SessaoTreino, error)
	// PerfilAtivo devolve o JSON da versão ativa; nil e 0 se não há nenhuma.
	PerfilAtivo(ctx context.Context, agente, tenant string) (perfilJSON []byte, versao int, err error)
	// Observacoes lista os padrões do tenant, com a evidência acumulada.
	Observacoes(ctx context.Context, agente, tenant string) ([]Observacao, error)
	// Notas lista os fatos do tenant, pendentes primeiro; estado vazio traz todos.
	Notas(ctx context.Context, agente, tenant, estado string) ([]Nota, error)
	// MarcarNota resolve uma nota pendente e devolve o estado final; nota já
	// resolvida não muda. ErrNaoEncontrado se for de outro tenant.
	MarcarNota(ctx context.Context, agente, tenant, id, estado string, em time.Time) (string, error)
	// Versoes lista o histórico do perfil, a mais nova primeiro.
	Versoes(ctx context.Context, agente, tenant string) ([]VersaoPerfil, error)
	// Versao devolve o JSON de uma versão; ErrNaoEncontrado se não existe.
	Versao(ctx context.Context, agente, tenant string, n int) ([]byte, error)
	// EditarPerfil aplica uma mudança do painel. ErrNaoEncontrado se alguma
	// observação não for do tenant.
	EditarPerfil(ctx context.Context, e Edicao) (ResultadoExtracao, error)
	// AplicarExtracao funde observações, cria notas, grava a versão nova se o
	// perfil mudou e marca a sessão como extraída, tudo numa transação: um retry
	// depois de queda não conta evidência duas vezes. ErrJaExtraida se já foi.
	AplicarExtracao(ctx context.Context, e Extracao) (ResultadoExtracao, error)
	// MarcarErroExtracao registra a tentativa que falhou.
	MarcarErroExtracao(ctx context.Context, id string) error
}
