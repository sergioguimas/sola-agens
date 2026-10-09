package entrada

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

func assinar(segredo, ts string, corpo []byte) string {
	m := hmac.New(sha256.New, []byte(segredo))
	m.Write([]byte(ts + "."))
	m.Write(corpo)
	return hex.EncodeToString(m.Sum(nil))
}

func TestVerificarAssinatura(t *testing.T) {
	agora := time.Unix(1_800_000_000, 0)
	corpo := []byte(`{"versao":1}`)
	seg := "segredo"
	ts := func(d time.Duration) string { return strconv.FormatInt(agora.Add(d).Unix(), 10) }

	outro := append([]byte(nil), corpo...)
	outro[2] ^= 1 // troca 1 byte

	cases := []struct {
		nome string
		ts   string
		corp []byte
		sig  string
		want error
	}{
		{"válida", ts(0), corpo, assinar(seg, ts(0), corpo), nil},
		{"válida no limite passado", ts(-300 * time.Second), corpo, assinar(seg, ts(-300*time.Second), corpo), nil},
		{"corpo com 1 byte trocado", ts(0), outro, assinar(seg, ts(0), corpo), ErrAssinaturaInvalida},
		{"segredo errado", ts(0), corpo, assinar("x", ts(0), corpo), ErrAssinaturaInvalida},
		{"301 s no passado", ts(-301 * time.Second), corpo, assinar(seg, ts(-301*time.Second), corpo), ErrTimestampForaJanela},
		{"301 s no futuro", ts(301 * time.Second), corpo, assinar(seg, ts(301*time.Second), corpo), ErrTimestampForaJanela},
		{"timestamp ausente", "", corpo, assinar(seg, "", corpo), ErrTimestampAusente},
		{"timestamp inválido", "abc", corpo, assinar(seg, "abc", corpo), ErrTimestampInvalido},
		{"assinatura ausente", ts(0), corpo, "", ErrAssinaturaInvalida},
		{"assinatura não-hex", ts(0), corpo, "zz", ErrAssinaturaInvalida},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			err := VerificarAssinatura([]byte(seg), c.ts, c.corp, c.sig, agora)
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func base() MensagemEncaminhada {
	m := MensagemEncaminhada{Versao: 1, MensagemID: "ABC", RecebidaEm: "2026-10-09T12:00:00.000Z"}
	m.Organizacao.ID = "org-1"
	m.Contato.Telefone = "5511999998888"
	return m
}

func TestNormalizar(t *testing.T) {
	nome := "Maria"
	t.Run("texto com ticket", func(t *testing.T) {
		m := base()
		m.Contato.NomeExibicao = &nome
		m.Conteudo = Conteudo{Tipo: "texto", Texto: "oi"}
		m.Ticket = &Ticket{Token: "tk", ExpiraEm: "2026-10-09T12:30:00Z"}
		e, err := Normalizar(m)
		if err != nil {
			t.Fatal(err)
		}
		if e.Tenant != "org-1" || e.Contato != "5511999998888" || e.Ticket != "tk" || e.DeMim ||
			e.NomeExibicao != "Maria" || e.Mensagem.Texto != "oi" || e.Mensagem.ID != "ABC" || len(e.Mensagem.Anexos) != 0 {
			t.Errorf("entrada = %+v", e)
		}
		if len(e.Conversa()) != 12 {
			t.Errorf("conversa = %q", e.Conversa())
		}
	})
	t.Run("áudio", func(t *testing.T) {
		m := base()
		m.Conteudo = Conteudo{Tipo: "audio", DuracaoS: 12, Mime: "audio/ogg; codecs=opus", Tamanho: 999, Encaminhado: true}
		e, err := Normalizar(m)
		if err != nil {
			t.Fatal(err)
		}
		a := e.Mensagem.Anexos
		if e.Mensagem.Texto != "" || len(a) != 1 || a[0].Tipo != "audio" || a[0].DuracaoS != 12 ||
			a[0].Tamanho != 999 || !a[0].Encaminhado || a[0].Ref != "ABC" || a[0].Mime != "audio/ogg; codecs=opus" {
			t.Errorf("anexos = %+v", a)
		}
	})
	t.Run("nao_suportado", func(t *testing.T) {
		m := base()
		m.Conteudo = Conteudo{Tipo: "nao_suportado", TipoOriginal: "imageMessage"}
		e, err := Normalizar(m)
		if err != nil {
			t.Fatal(err)
		}
		a := e.Mensagem.Anexos
		if len(a) != 1 || a[0].Tipo != "outro" || a[0].TipoOriginal != "imageMessage" {
			t.Errorf("anexos = %+v", a)
		}
	})
	t.Run("deMim zera o ticket", func(t *testing.T) {
		m := base()
		m.DeMim = true
		m.Conteudo = Conteudo{Tipo: "texto", Texto: "x"}
		m.Ticket = &Ticket{Token: "tk"}
		e, _ := Normalizar(m)
		if e.Ticket != "" || !e.DeMim {
			t.Errorf("entrada = %+v", e)
		}
	})
}

func TestValidar(t *testing.T) {
	cases := map[string]func(*MensagemEncaminhada){
		"versao":         func(m *MensagemEncaminhada) { m.Versao = 2 },
		"mensagemId":     func(m *MensagemEncaminhada) { m.MensagemID = "" },
		"recebidaEm":     func(m *MensagemEncaminhada) { m.RecebidaEm = "ontem" },
		"organizacao":    func(m *MensagemEncaminhada) { m.Organizacao.ID = "" },
		"telefone":       func(m *MensagemEncaminhada) { m.Contato.Telefone = "" },
		"tipo":           func(m *MensagemEncaminhada) { m.Conteudo.Tipo = "video" },
		"texto vazio":    func(m *MensagemEncaminhada) { m.Conteudo = Conteudo{Tipo: "texto"} },
		"áudio sem mime": func(m *MensagemEncaminhada) { m.Conteudo = Conteudo{Tipo: "audio", DuracaoS: 1} },
	}
	for nome, mut := range cases {
		m := base()
		m.Conteudo = Conteudo{Tipo: "texto", Texto: "oi"}
		mut(&m)
		if err := m.Validar(); !errors.Is(err, ErrCorpoInvalido) {
			t.Errorf("%s: err = %v", nome, err)
		}
	}
}
