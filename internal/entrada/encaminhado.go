// Package entrada trata a mensagem encaminhada pelo sistema (modo
// "encaminhado"): corpo, assinatura HMAC e normalização para o domínio.
// Contrato: specs/04 §1; Zod de referência em
// Eliza/web/contracts/autoatendimento/mensagens.ts.
package entrada

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sola-agens/internal/mensagem"
)

// JanelaTimestamp é a tolerância da assinatura, nos dois sentidos.
const JanelaTimestamp = 300 * time.Second

// Erros de assinatura, distintos para o log.
var (
	ErrTimestampAusente    = errors.New("timestamp ausente")
	ErrTimestampInvalido   = errors.New("timestamp inválido")
	ErrTimestampForaJanela = errors.New("timestamp fora da janela")
	ErrAssinaturaInvalida  = errors.New("assinatura inválida")
	ErrCorpoInvalido       = errors.New("corpo inválido")
)

// Conteudo é a união discriminada por Tipo (texto | audio | nao_suportado),
// achatada: cada tipo usa só os seus campos.
type Conteudo struct {
	Tipo         string `json:"tipo"`
	Texto        string `json:"texto"`
	DuracaoS     int    `json:"duracaoS"`
	Mime         string `json:"mime"`
	Tamanho      int64  `json:"tamanho"`
	Encaminhado  bool   `json:"encaminhado"`
	TipoOriginal string `json:"tipoOriginal"`
}

// Ticket emitido pelo sistema; opaco para o agens.
type Ticket struct {
	Token    string `json:"token"`
	ExpiraEm string `json:"expiraEm"`
}

// MensagemEncaminhada espelha o Zod do Eliza. Campos desconhecidos são
// ignorados (encoding/json já faz isso por padrão).
type MensagemEncaminhada struct {
	Versao      int    `json:"versao"`
	MensagemID  string `json:"mensagemId"`
	RecebidaEm  string `json:"recebidaEm"`
	Organizacao struct {
		ID   string `json:"id"`
		Nome string `json:"nome"`
	} `json:"organizacao"`
	Contato struct {
		Telefone     string  `json:"telefone"`
		NomeExibicao *string `json:"nomeExibicao"`
	} `json:"contato"`
	DeMim    bool     `json:"deMim"`
	Conteudo Conteudo `json:"conteudo"`
	Ticket   *Ticket  `json:"ticket"`
}

// Entrada é a mensagem normalizada mais a identidade (specs/01 §1).
type Entrada struct {
	Tenant       string
	Contato      string
	Ticket       string // vazio quando DeMim
	DeMim        bool
	NomeExibicao string
	Mensagem     mensagem.Mensagem
}

// Conversa devolve sha256(contato) em hex, 12 primeiros caracteres: o único
// identificador do contato permitido em log (specs/05 §8).
func (e Entrada) Conversa() string { return HashContato(e.Contato) }

// HashContato é sha256(contato)[:12] em hex.
func HashContato(contato string) string {
	h := sha256.Sum256([]byte(contato))
	return hex.EncodeToString(h[:])[:12]
}

// VerificarAssinatura confere hex(HMAC-SHA256(segredo, timestamp+"."+corpo)).
// O timestamp é epoch em segundos; tolerância de JanelaTimestamp para os dois lados.
func VerificarAssinatura(segredo []byte, timestamp string, corpo []byte, assinaturaHex string, agora time.Time) error {
	timestamp = strings.TrimSpace(timestamp)
	if timestamp == "" {
		return ErrTimestampAusente
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrTimestampInvalido
	}
	delta := agora.Sub(time.Unix(ts, 0))
	if delta > JanelaTimestamp || delta < -JanelaTimestamp {
		return ErrTimestampForaJanela
	}

	mac := hmac.New(sha256.New, segredo)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(corpo)
	esperado := mac.Sum(nil)

	recebido, err := hex.DecodeString(strings.TrimSpace(assinaturaHex))
	if err != nil || len(recebido) == 0 || !hmac.Equal(esperado, recebido) {
		return ErrAssinaturaInvalida
	}
	return nil
}

// Validar confere o que o Zod exige (versao 1, campos obrigatórios, união).
func (m *MensagemEncaminhada) Validar() error {
	bad := func(f string) error { return fmt.Errorf("%w: %s", ErrCorpoInvalido, f) }
	if m.Versao != 1 {
		return bad("versao")
	}
	if m.MensagemID == "" {
		return bad("mensagemId")
	}
	if _, err := time.Parse(time.RFC3339, m.RecebidaEm); err != nil {
		return bad("recebidaEm")
	}
	if m.Organizacao.ID == "" {
		return bad("organizacao.id")
	}
	if m.Contato.Telefone == "" {
		return bad("contato.telefone")
	}
	switch c := m.Conteudo; c.Tipo {
	case "texto":
		if c.Texto == "" {
			return bad("conteudo.texto")
		}
	case "audio":
		if c.Mime == "" || c.DuracaoS < 0 || c.Tamanho < 0 {
			return bad("conteudo (audio)")
		}
	case "nao_suportado":
	default:
		return bad("conteudo.tipo")
	}
	return nil
}

// Normalizar converte para o tipo de domínio. Chame Validar antes.
func Normalizar(m MensagemEncaminhada) (Entrada, error) {
	if err := m.Validar(); err != nil {
		return Entrada{}, err
	}
	recebida, _ := time.Parse(time.RFC3339, m.RecebidaEm) // já validado
	nome := ""
	if m.Contato.NomeExibicao != nil {
		nome = *m.Contato.NomeExibicao
	}
	msg := mensagem.Mensagem{ID: m.MensagemID, RecebidaEm: recebida, NomeExibicao: nome}
	switch c := m.Conteudo; c.Tipo {
	case "texto":
		msg.Texto = c.Texto
	case "audio":
		msg.Anexos = []mensagem.Anexo{{
			Tipo: "audio", Mime: c.Mime, Tamanho: c.Tamanho, DuracaoS: c.DuracaoS,
			Encaminhado: c.Encaminhado, Ref: m.MensagemID,
		}}
	case "nao_suportado":
		msg.Anexos = []mensagem.Anexo{{Tipo: "outro", TipoOriginal: c.TipoOriginal, Ref: m.MensagemID}}
	}
	e := Entrada{
		Tenant:       m.Organizacao.ID,
		Contato:      m.Contato.Telefone,
		DeMim:        m.DeMim,
		NomeExibicao: nome,
		Mensagem:     msg,
	}
	if m.Ticket != nil && !m.DeMim {
		e.Ticket = m.Ticket.Token
	}
	return e, nil
}
