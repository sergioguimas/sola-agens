package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"net/http/httptest"

	"sola-agens/internal/agent"
	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
)

const (
	rotaWeb   = "/a/eliza/v1/web"
	yamlWeb   = yamlTeste + "web: {}\n"
	contatoOk = "web:22222222-2222-4222-8222-222222222222"
)

type webFalso struct {
	err error
	n   int
}

func (f *webFalso) TurnoWeb(context.Context, agentes.Agente, entrada.MensagemWeb) (entrada.RespostaWeb, error) {
	f.n++
	if f.err != nil {
		return entrada.RespostaWeb{}, f.err
	}
	return entrada.RespostaWeb{Resposta: "Oi!", Acoes: []entrada.AcaoWeb{}}, nil
}

func corpoWeb(mensagemID, contato, texto string) string {
	return fmt.Sprintf(`{"versao":1,"canal":"web","mensagemId":%q,"orgId":"org-1","contato":%q,"texto":%q,
"recebidaEm":"2026-11-04T12:00:00.000Z","ticket":{"token":"tk","expiraEm":"2026-11-04T12:30:00.000Z"},
"formulario":{"etapa":1,"servicoId":null,"profissionalId":null,"data":null,"hora":null}}`, mensagemID, contato, texto)
}

func enviarWeb(s *Server, corpo, sig string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", rotaWeb, strings.NewReader(corpo))
	r.Header.Set("X-Ts", strconv.FormatInt(agora.Unix(), 10))
	r.Header.Set("X-Sig", sig)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	return rec
}

func montarWeb(t *testing.T) (*Server, *webFalso) {
	t.Helper()
	s := montarTreino(t, yamlWeb)
	f := &webFalso{}
	s.CanalWeb = f
	s.Eventos = abrirStore(t)
	return s, f
}

func TestWebRespondeNoCorpo(t *testing.T) {
	s, f := montarWeb(t)
	c := corpoWeb("m1", contatoOk, "oi")
	rec := enviarWeb(s, c, assinar(c, agora.Unix()))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"acoes":[]`) || f.n != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestWebDesligado(t *testing.T) {
	s := montarTreino(t, yamlTeste)
	s.CanalWeb = &webFalso{}
	c := corpoWeb("m1", contatoOk, "oi")
	if rec := enviarWeb(s, c, assinar(c, agora.Unix())); rec.Code != 404 || codigoErro(t, rec) != "WEB_DESLIGADO" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestWebAssinaturaEDuplicada(t *testing.T) {
	s, f := montarWeb(t)
	c := corpoWeb("m1", contatoOk, "oi")
	if rec := enviarWeb(s, c, "00"); rec.Code != 401 {
		t.Fatalf("assinatura: %d", rec.Code)
	}
	sig := assinar(c, agora.Unix())
	if rec := enviarWeb(s, c, sig); rec.Code != 200 {
		t.Fatalf("primeira: %d", rec.Code)
	}
	if rec := enviarWeb(s, c, sig); rec.Code != 409 || f.n != 1 {
		t.Fatalf("duplicada: %d chamadas=%d", rec.Code, f.n)
	}
}

func TestWebCorpoInvalido(t *testing.T) {
	s, f := montarWeb(t)
	casos := map[string]string{
		"contato de telefone": corpoWeb("m1", "5511999990000", "oi"),
		"texto vazio":         corpoWeb("m2", contatoOk, "   "),
		"texto longo":         corpoWeb("m3", contatoOk, strings.Repeat("x", 501)),
		"json quebrado":       `{`,
		"sem ticket":          strings.Replace(corpoWeb("m4", contatoOk, "oi"), `"ticket":{"token":"tk",`, `"ticket":{"token":"",`, 1),
		"canal errado":        strings.Replace(corpoWeb("m5", contatoOk, "oi"), `"canal":"web"`, `"canal":"whatsapp"`, 1),
	}
	for nome, c := range casos {
		if rec := enviarWeb(s, c, assinar(c, agora.Unix())); rec.Code != 400 {
			t.Errorf("%s: %d", nome, rec.Code)
		}
	}
	if f.n != 0 {
		t.Errorf("chegou ao turno: %d", f.n)
	}
}

func TestWebErrosDoTurno(t *testing.T) {
	casos := []struct {
		err    error
		status int
		codigo string
	}{
		{agent.ErrTetoWeb, 429, "TETO_DIARIO"},
		{agent.ErrWebIndisponivel, 503, "INDISPONIVEL"},
		{errors.New("qualquer"), 503, "INDISPONIVEL"},
	}
	for i, c := range casos {
		s, f := montarWeb(t)
		f.err = c.err
		corpo := corpoWeb(fmt.Sprintf("m%d", i), contatoOk, "oi")
		rec := enviarWeb(s, corpo, assinar(corpo, agora.Unix()))
		if rec.Code != c.status || codigoErro(t, rec) != c.codigo {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "qualquer") {
			t.Errorf("vazou detalhe: %s", rec.Body.String())
		}
	}
}
