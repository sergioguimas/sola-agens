package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
)

const (
	segredo  = "segredo-de-teste"
	telefone = "5511987654321"
	textoMsg = "quero cortar o cabelo amanha"
)

var agora = time.Unix(1_800_000_000, 0)

type fakeHandler struct {
	msgs, demim []entrada.Entrada
	err         error
}

func (f *fakeHandler) Mensagem(_ context.Context, _ agentes.Agente, e entrada.Entrada) error {
	f.msgs = append(f.msgs, e)
	return f.err
}
func (f *fakeHandler) DeMim(_ context.Context, _ agentes.Agente, e entrada.Entrada) error {
	f.demim = append(f.demim, e)
	return f.err
}

const yamlTeste = `id: eliza
modo_identidade: encaminhado
entrada: {segredo_env: S, header_assinatura: X-Sig, header_timestamp: X-Ts}
sistema: {base_url_env: U, openapi_path: /o.json, token_env: T, header_ticket: X-Tk}
operacoes: {contexto: a, responder: b, escalar: c, midia: d}
`

func montar(t *testing.T) (*Server, *fakeHandler, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "eliza.yaml"), []byte(yamlTeste), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"S": segredo, "U": "http://x", "T": "tok"}
	ags, err := agentes.Carregar(dir, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	h := &fakeHandler{}
	s := New(ags, h, slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	s.Agora = func() time.Time { return agora }
	return s, h, buf
}

func corpoJSON(deMim bool, ticket bool) string {
	tk := `null`
	if ticket {
		tk = `{"token":"tk-secreto","expiraEm":"2026-10-09T12:30:00Z"}`
	}
	return fmt.Sprintf(`{"versao":1,"mensagemId":"M1","recebidaEm":"2026-10-09T12:00:00Z",
"organizacao":{"id":"org-1","nome":"Salao"},"contato":{"telefone":%q,"nomeExibicao":null},
"deMim":%v,"conteudo":{"tipo":"texto","texto":%q},"ticket":%s,"campoNovo":1}`, telefone, deMim, textoMsg, tk)
}

func requisicao(path, corpo string, ts int64, sig string) *http.Request {
	r := httptest.NewRequest("POST", path, strings.NewReader(corpo))
	if ts != 0 {
		r.Header.Set("X-Ts", strconv.FormatInt(ts, 10))
	}
	if sig != "" {
		r.Header.Set("X-Sig", sig)
	}
	return r
}

func assinar(corpo string, ts int64) string {
	m := hmac.New(sha256.New, []byte(segredo))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "." + corpo))
	return hex.EncodeToString(m.Sum(nil))
}

func enviar(s *Server, path, corpo string, ts int64, sig string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, requisicao(path, corpo, ts, sig))
	return rec
}

const rota = "/a/eliza/v1/mensagens"

func TestHealth(t *testing.T) {
	s, _, _ := montar(t)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("health = %d %q", rec.Code, rec.Body.String())
	}
}

func TestMensagens(t *testing.T) {
	ts := agora.Unix()
	ok := corpoJSON(false, true)
	semTicket := corpoJSON(false, false)
	deMim := corpoJSON(true, false)
	v2 := strings.Replace(ok, `"versao":1`, `"versao":2`, 1)
	grande := strings.Repeat("a", MaxCorpo+1)

	cases := []struct {
		nome     string
		path     string
		corpo    string
		ts       int64
		sig      string
		want     int
		msgs, dm int
	}{
		{"agente desconhecido", "/a/outro/v1/mensagens", ok, ts, assinar(ok, ts), 404, 0, 0},
		{"corpo grande", rota, grande, ts, assinar(grande, ts), 413, 0, 0},
		{"sem timestamp", rota, ok, 0, assinar(ok, ts), 401, 0, 0},
		{"timestamp velho", rota, ok, ts - 301, assinar(ok, ts-301), 401, 0, 0},
		{"assinatura errada", rota, ok, ts, assinar(ok+" ", ts), 401, 0, 0},
		{"sem assinatura", rota, ok, ts, "", 401, 0, 0},
		{"json inválido", rota, "{nao", ts, assinar("{nao", ts), 400, 0, 0},
		{"versao 2", rota, v2, ts, assinar(v2, ts), 400, 0, 0},
		{"sem ticket", rota, semTicket, ts, assinar(semTicket, ts), 400, 0, 0},
		{"ok", rota, ok, ts, assinar(ok, ts), 202, 1, 0},
		{"deMim", rota, deMim, ts, assinar(deMim, ts), 202, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			s, h, _ := montar(t)
			rec := enviar(s, c.path, c.corpo, c.ts, c.sig)
			if rec.Code != c.want {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), c.want)
			}
			if len(h.msgs) != c.msgs || len(h.demim) != c.dm {
				t.Errorf("handler: msgs=%d demim=%d", len(h.msgs), len(h.demim))
			}
			if c.want == 202 && strings.TrimSpace(rec.Body.String()) != `{"aceito":true}` {
				t.Errorf("corpo = %q", rec.Body.String())
			}
		})
	}
}

func TestEntradaChegaNormalizada(t *testing.T) {
	s, h, _ := montar(t)
	ok := corpoJSON(false, true)
	enviar(s, rota, ok, agora.Unix(), assinar(ok, agora.Unix()))
	if len(h.msgs) != 1 {
		t.Fatal("sem entrega")
	}
	e := h.msgs[0]
	if e.Tenant != "org-1" || e.Contato != telefone || e.Ticket != "tk-secreto" || e.Mensagem.Texto != textoMsg {
		t.Errorf("entrada = %+v", e)
	}
}

func TestFilaCheiaEErroDoHandler(t *testing.T) {
	ok := corpoJSON(false, true)
	ts := agora.Unix()
	for erro, want := range map[error]int{
		ErrFilaCheia: 503,
		fmt.Errorf("embrulhado: %w", ErrFilaCheia): 503,
		errors.New("outro"):                        500,
	} {
		s, h, _ := montar(t)
		h.err = erro
		if rec := enviar(s, rota, ok, ts, assinar(ok, ts)); rec.Code != want {
			t.Errorf("%v: status = %d, want %d", erro, rec.Code, want)
		}
	}
}

func TestDedupe(t *testing.T) {
	s, h, _ := montar(t)
	s.Dedupe = func(context.Context, string, string) (bool, error) { return true, nil }
	ok := corpoJSON(false, true)
	rec := enviar(s, rota, ok, agora.Unix(), assinar(ok, agora.Unix()))
	if rec.Code != 202 || len(h.msgs) != 0 {
		t.Errorf("duplicado: status=%d msgs=%d", rec.Code, len(h.msgs))
	}
}

func TestLogsSemDadosSensiveis(t *testing.T) {
	ts := agora.Unix()
	s, _, buf := montar(t)
	ok := corpoJSON(false, true)
	deMim := corpoJSON(true, false)
	semTicket := corpoJSON(false, false)
	enviar(s, rota, ok, ts, assinar(ok, ts))
	enviar(s, rota, deMim, ts, assinar(deMim, ts))
	enviar(s, rota, semTicket, ts, assinar(semTicket, ts))
	enviar(s, rota, ok, ts-999, assinar(ok, ts-999))
	enviar(s, rota, ok, ts, "00")

	log := buf.String()
	if log == "" {
		t.Fatal("esperava algum log")
	}
	for _, proibido := range []string{telefone, textoMsg, "tk-secreto", segredo, "tok"} {
		if strings.Contains(log, proibido) {
			t.Errorf("log contém %q", proibido)
		}
	}
	if !strings.Contains(log, entrada.HashContato(telefone)) {
		t.Error("log deveria ter o hash da conversa")
	}
}
