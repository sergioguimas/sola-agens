package entrada

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// TextoWebMax é o limite do Zod do Eliza (CHAT_TEXTO_MAX, contrato 08 §6.1).
const TextoWebMax = 500

var (
	contatoWeb = regexp.MustCompile(`^web:[0-9a-f-]{36}$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	dataRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	horaRe     = regexp.MustCompile(`^\d{2}:\d{2}$`)
)

// ErrWebInvalida: a mensagem não respeita o contrato do canal web.
var ErrWebInvalida = errors.New("mensagem web inválida")

// FormularioWeb é o estado do formulário: dica, não autoriza nada (contrato 08 §6.1).
type FormularioWeb struct {
	Etapa          int     `json:"etapa"`
	ServicoID      *string `json:"servicoId"`
	ProfissionalID *string `json:"profissionalId"`
	Data           *string `json:"data"`
	Hora           *string `json:"hora"`
}

// MensagemWeb é o corpo de POST /a/{agente}/v1/web.
type MensagemWeb struct {
	Versao     int           `json:"versao"`
	Canal      string        `json:"canal"`
	MensagemID string        `json:"mensagemId"`
	OrgID      string        `json:"orgId"`
	Contato    string        `json:"contato"`
	Texto      string        `json:"texto"`
	RecebidaEm time.Time     `json:"recebidaEm"`
	Ticket     *Ticket       `json:"ticket"`
	Formulario FormularioWeb `json:"formulario"`
}

// AcaoWeb preenche o formulário; só os campos escolhidos vão no JSON. É o Zod
// AcaoPreencherAgendamento do Eliza, que recusa campo a mais.
type AcaoWeb struct {
	Tipo           string `json:"tipo"`
	ServicoID      string `json:"servicoId,omitempty"`
	ProfissionalID string `json:"profissionalId,omitempty"`
	Data           string `json:"data,omitempty"`
	Hora           string `json:"hora,omitempty"`
}

// RespostaWeb é o corpo da resposta do canal.
type RespostaWeb struct {
	Resposta string    `json:"resposta"`
	Acoes    []AcaoWeb `json:"acoes"`
}

// LerMensagemWeb decodifica e valida o corpo.
func LerMensagemWeb(corpo []byte) (MensagemWeb, error) {
	var m MensagemWeb
	if err := json.Unmarshal(corpo, &m); err != nil {
		return m, ErrWebInvalida
	}
	m.Texto = strings.TrimSpace(m.Texto)
	switch {
	case m.Versao != 1, m.Canal != "web", m.OrgID == "", m.MensagemID == "":
		return m, ErrWebInvalida
	case !contatoWeb.MatchString(m.Contato):
		return m, ErrWebInvalida
	case m.Texto == "", utf8.RuneCountInString(m.Texto) > TextoWebMax:
		return m, ErrWebInvalida
	case m.Ticket == nil || m.Ticket.Token == "":
		return m, ErrWebInvalida
	}
	return m, nil
}

// EhUUID, EhData e EhHora validam o que vai numa ação: o Zod do Eliza recusa a
// resposta inteira se um campo vier fora de formato.
func EhUUID(s string) bool { return uuidRe.MatchString(s) }

// EhData confere AAAA-MM-DD, inclusive se a data existe.
func EhData(s string) bool {
	if !dataRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// EhHora confere HH:MM.
func EhHora(s string) bool {
	if !horaRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("15:04", s)
	return err == nil
}
