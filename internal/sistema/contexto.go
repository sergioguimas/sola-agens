package sistema

// Structs espelhando web/contracts/autoatendimento/contexto.ts do Eliza. Campos
// desconhecidos são ignorados (o Eliza pode acrescentar sem quebrar o agens) e
// campos ausentes ficam no zero value (ex.: `atendente` antes do contrato 07).

// Momento é um horário nas duas formas: UTC (ISO) e relógio local sem offset.
type Momento struct {
	UTC   string `json:"utc"`
	Local string `json:"local"`
}

// Termos é o vocabulário do nicho.
type Termos struct {
	Genero             string `json:"genero"` // "m" ou "f"
	Cliente            string `json:"cliente"`
	ClientePlural      string `json:"clientePlural"`
	Profissional       string `json:"profissional"`
	ProfissionalPlural string `json:"profissionalPlural"`
	Servico            string `json:"servico"`
	ServicoPlural      string `json:"servicoPlural"`
	Agendamento        string `json:"agendamento"`
	AgendamentoPlural  string `json:"agendamentoPlural"`
}

type Organizacao struct {
	Nome   string `json:"nome"`
	Nicho  string `json:"nicho"`
	Termos Termos `json:"termos"`
}

type Almoco struct {
	Inicio string `json:"inicio"` // HH:mm
	Fim    string `json:"fim"`
}

type Expediente struct {
	DiasDaSemana []int   `json:"diasDaSemana"` // 0 = domingo … 6 = sábado
	Abertura     *string `json:"abertura"`
	Fechamento   *string `json:"fechamento"`
	Almoco       *Almoco `json:"almoco"`
}

type Politica struct {
	AntecedenciaMinimaMinutos int     `json:"antecedenciaMinimaMinutos"`
	JanelaMaximaDias          int     `json:"janelaMaximaDias"`
	MaxAgendamentosAtivos     int     `json:"maxAgendamentosAtivos"`
	InstrucoesAtendimento     *string `json:"instrucoesAtendimento"`
}

// Situações de Identificacao.Situacao.
const (
	SituacaoIdentificado = "identificado"
	SituacaoDesconhecido = "desconhecido"
	SituacaoAmbiguo      = "ambiguo"
)

type ClienteIdentificado struct {
	ID           string `json:"id"`
	PrimeiroNome string `json:"primeiroNome"`
}

type Identificacao struct {
	Situacao string               `json:"situacao"`
	Cliente  *ClienteIdentificado `json:"cliente"` // só em "identificado"
}

// Ref é {id, nome} de serviço ou profissional.
type Ref struct {
	ID   string `json:"id"`
	Nome string `json:"nome"`
}

// AgendamentoResumo (contracts/autoatendimento/agendamentos.ts).
type AgendamentoResumo struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	Inicio        Momento `json:"inicio"`
	Fim           Momento `json:"fim"`
	Servico       *Ref    `json:"servico"`
	Profissional  *Ref    `json:"profissional"`
	PodeCancelar  bool    `json:"podeCancelar"`
	PodeRemarcar  bool    `json:"podeRemarcar"`
	PodeConfirmar bool    `json:"podeConfirmar"`
}

// Respostas são os textos fixos do atendente; nil = padrão do motor.
type Respostas struct {
	Confirmado        *string `json:"confirmado"`
	Cancelado         *string `json:"cancelado"`
	FalhaTecnica      *string `json:"falhaTecnica"`
	MidiaNaoSuportada *string `json:"midiaNaoSuportada"`
	AudioLongo        *string `json:"audioLongo"`
	EquipeSemContato  *string `json:"equipeSemContato"`
}

// Palavras do fastpath; nil = padrão do motor.
type Palavras struct {
	Confirmar []string `json:"confirmar"`
	Cancelar  []string `json:"cancelar"`
}

// Atendente é a personalização por org (todos anuláveis). Ausente no JSON =
// zero value = tudo no padrão do motor.
type Atendente struct {
	Persona          *string   `json:"persona"`
	Tom              *string   `json:"tom"`
	PausaHumanaHoras *int      `json:"pausaHumanaHoras"`
	Respostas        Respostas `json:"respostas"`
	Palavras         Palavras  `json:"palavras"`
}

// Contexto do turno (GET /contexto).
type Contexto struct {
	Agora                 Momento             `json:"agora"`
	FusoHorario           string              `json:"fusoHorario"`
	Organizacao           Organizacao         `json:"organizacao"`
	Expediente            Expediente          `json:"expediente"`
	Politica              Politica            `json:"politica"`
	Identificacao         Identificacao       `json:"identificacao"`
	Agendamentos          []AgendamentoResumo `json:"agendamentos"`
	AguardandoConfirmacao []string            `json:"aguardandoConfirmacao"`
	Atendente             Atendente           `json:"atendente"`
}
