package conector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sola-agens/internal/llm"
	"sola-agens/internal/openapi"
)

func novo(srv *httptest.Server) (*HTTP, *[]time.Duration) {
	var pausas []time.Duration
	h := NovoHTTP(Config{
		BaseURL: srv.URL, Token: "tok-servico", HeaderTicket: "X-Autoatendimento-Ticket",
		HeadersFixos: map[string]string{"X-Autoatendimento-Versao": "1"},
	}, 2*time.Second)
	h.Cliente = srv.Client()
	h.Esperar = func(_ context.Context, d time.Duration) error { pausas = append(pausas, d); return nil }
	return h, &pausas
}

func ferr(metodo, caminho string, body bool, args map[string]openapi.Destino) openapi.Ferramenta {
	return openapi.Ferramenta{Decl: llm.ToolDecl{Name: "t"}, Metodo: metodo, Caminho: caminho, Args: args, BodyObrigatorio: body}
}

func envErro(w http.ResponseWriter, status int, code string, details string) {
	w.WriteHeader(status)
	d := ""
	if details != "" {
		d = `,"details":` + details
	}
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"msg ` + code + `"` + d + `,"request_id":"req-1"}}`))
}

func TestMontaPathQueryBodyEHeaders(t *testing.T) {
	var gotPath, gotQuery, gotBody string
	var gotHdr http.Header
	var gotMetodo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMetodo, gotPath, gotQuery, gotHdr = r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()
	h, _ := novo(srv)

	f := ferr("POST", "/ag/{id}/remarcar", true, map[string]openapi.Destino{
		"id": openapi.DestinoPath, "dia": openapi.DestinoQuery, "n": openapi.DestinoQuery, "inicio": openapi.DestinoBody, "prof": openapi.DestinoBody,
	})
	res, err := h.Executar(context.Background(), f, map[string]any{
		"id": "a/b c", "dia": "2026-10-05", "n": 10000000.0, "inicio": "2026-10-05T15:00", "prof": nil,
		"inventado": "x",
	}, Identidade{Ticket: "tkt-123"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMetodo != "POST" || gotPath != "/ag/a%2Fb%20c/remarcar" {
		t.Errorf("método/path = %s %s", gotMetodo, gotPath)
	}
	if gotQuery != "dia=2026-10-05&n=10000000" {
		t.Errorf("query = %s", gotQuery)
	}
	if gotBody != `{"inicio":"2026-10-05T15:00"}` {
		t.Errorf("body = %s", gotBody)
	}
	for k, v := range map[string]string{
		"Authorization": "Bearer tok-servico", "X-Autoatendimento-Ticket": "tkt-123",
		"X-Autoatendimento-Versao": "1", "Content-Type": "application/json",
	} {
		if gotHdr.Get(k) != v {
			t.Errorf("header %s = %q, want %q", k, gotHdr.Get(k), v)
		}
	}
	if len(res.Avisos) != 1 || res.Avisos[0] != "campo inventado ignorado" {
		t.Errorf("avisos = %v", res.Avisos)
	}
	if string(res.Dados) != `{"ok":true}` {
		t.Errorf("dados = %s", res.Dados)
	}
}

func TestBodyOpcionalEObrigatorio(t *testing.T) {
	var gotBody string
	var ct string
	var tinhaBody bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, ct, tinhaBody = string(b), r.Header.Get("Content-Type"), len(b) > 0
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	h, _ := novo(srv)
	args := map[string]openapi.Destino{"motivo": openapi.DestinoBody}

	if _, err := h.Executar(context.Background(), ferr("POST", "/x", false, args), nil, Identidade{}); err != nil {
		t.Fatal(err)
	}
	if tinhaBody || ct != "" {
		t.Errorf("body opcional sem campos deveria ir sem corpo: %q %q", gotBody, ct)
	}
	if _, err := h.Executar(context.Background(), ferr("POST", "/x", true, args), nil, Identidade{}); err != nil {
		t.Fatal(err)
	}
	if gotBody != "{}" || ct != "application/json" {
		t.Errorf("body obrigatório: %q %q", gotBody, ct)
	}
	if _, err := h.Executar(context.Background(), ferr("POST", "/x", false, args), map[string]any{"motivo": "chuva"}, Identidade{}); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"motivo":"chuva"}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestPathAusenteNaoChamaOSistema(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1) }))
	defer srv.Close()
	h, _ := novo(srv)
	f := ferr("POST", "/ag/{id}/cancelar", false, map[string]openapi.Destino{"id": openapi.DestinoPath})
	for _, args := range []map[string]any{nil, {"id": ""}, {"id": ".."}} {
		res, err := h.Executar(context.Background(), f, args, Identidade{})
		if err != nil || res.ErroRegra == nil {
			t.Errorf("args=%v res=%+v err=%v", args, res, err)
		}
	}
	if n.Load() != 0 {
		t.Errorf("chamadas = %d", n.Load())
	}
}

func TestRespostas(t *testing.T) {
	cases := []struct {
		nome      string
		status    int
		code      string
		details   string
		wantRegra string
		wantAbort string
	}{
		{"422 validação vira erro de regra", 422, "VALIDATION_ERROR", `{"campo":"inicio"}`, "VALIDATION_ERROR", ""},
		{"409 horário ocupado", 409, "SLOT_UNAVAILABLE", `{"sugestoes":["15:30"]}`, "SLOT_UNAVAILABLE", ""},
		{"404 não encontrado", 404, "NOT_FOUND", "", "NOT_FOUND", ""},
		{"401 ticket expirado aborta", 401, "TICKET_EXPIRED", "", "", "TICKET_EXPIRED"},
		{"401 não autorizado aborta", 401, "UNAUTHORIZED", "", "", "UNAUTHORIZED"},
		{"429 aborta", 429, "RATE_LIMITED", "", "", "RATE_LIMITED"},
		{"403 addon aborta", 403, "ADDON_INACTIVE", "", "", "ADDON_INACTIVE"},
		{"400 versão aborta", 400, "VERSION_MISMATCH", "", "", "VERSION_MISMATCH"},
		{"500 aborta", 500, "INTERNAL_ERROR", "", "", "INTERNAL_ERROR"},
		{"502 com código de regra ainda aborta", 502, "WHATSAPP_UNAVAILABLE", "", "", "WHATSAPP_UNAVAILABLE"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				envErro(w, c.status, c.code, c.details)
			}))
			defer srv.Close()
			h, _ := novo(srv)
			res, err := h.Executar(context.Background(), ferr("POST", "/x", false, nil), nil, Identidade{})
			if c.wantAbort != "" {
				var ab *ErrAbortar
				if !errors.As(err, &ab) || ab.Code != c.wantAbort || ab.Status != c.status || ab.RequestID != "req-1" {
					t.Fatalf("err = %#v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.ErroRegra == nil || res.ErroRegra.Erro != c.wantRegra || res.RequestID != "req-1" {
				t.Fatalf("res = %+v", res)
			}
			m := res.ParaLLM()
			if m["erro"] != c.wantRegra || m["mensagem"] != "msg "+c.code {
				t.Errorf("ParaLLM = %v", m)
			}
			if (c.details != "") != (m["detalhes"] != nil) {
				t.Errorf("detalhes = %v", m["detalhes"])
			}
		})
	}
}

func TestGETRetentaUmaVez(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			envErro(w, 503, "INTERNAL_ERROR", "")
			return
		}
		_, _ = w.Write([]byte(`{"data":{"servicos":[]},"meta":{"x":1}}`))
	}))
	defer srv.Close()
	h, pausas := novo(srv)
	res, err := h.Executar(context.Background(), ferr("GET", "/s", false, nil), nil, Identidade{})
	if err != nil || string(res.Dados) != `{"servicos":[]}` {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if n.Load() != 2 || len(*pausas) != 1 || (*pausas)[0] != 500*time.Millisecond {
		t.Errorf("chamadas=%d pausas=%v", n.Load(), *pausas)
	}
}

func TestGETRetentaSoUmaVezMesmoFalhando(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	h, _ := novo(srv)
	_, err := h.Executar(context.Background(), ferr("GET", "/s", false, nil), nil, Identidade{})
	var ab *ErrAbortar
	if !errors.As(err, &ab) || ab.Status != 503 {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 2 {
		t.Errorf("chamadas = %d, want 2", n.Load())
	}
}

func TestEscritaNuncaRetenta(t *testing.T) {
	for _, m := range []string{"POST", "PATCH", "DELETE"} {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			envErro(w, 503, "INTERNAL_ERROR", "")
		}))
		h, _ := novo(srv)
		_, err := h.Executar(context.Background(), ferr(m, "/x", false, nil), nil, Identidade{})
		var ab *ErrAbortar
		if !errors.As(err, &ab) || n.Load() != 1 {
			t.Errorf("%s: err=%v chamadas=%d", m, err, n.Load())
		}
		srv.Close()
	}
}

func TestCorpoNaoEnvelope(t *testing.T) {
	for nome, corpo := range map[string]string{"html": "<html>oops</html>", "vazio": "", "sem data": `{"ok":true}`} {
		t.Run(nome, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(corpo))
			}))
			defer srv.Close()
			h, _ := novo(srv)
			_, err := h.Executar(context.Background(), ferr("POST", "/x", false, nil), nil, Identidade{})
			var ab *ErrAbortar
			if !errors.As(err, &ab) || ab.Code != "NAO_ENVELOPE" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestErroNaoEnvelope4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found"))
	}))
	defer srv.Close()
	h, _ := novo(srv)
	_, err := h.Executar(context.Background(), ferr("POST", "/x", false, nil), nil, Identidade{})
	var ab *ErrAbortar
	if !errors.As(err, &ab) || ab.Code != "NAO_ENVELOPE" || ab.Status != 404 {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeoutAbortaEErroDeRedeNaoVazaURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	h, _ := novo(srv)
	h.Timeout = 50 * time.Millisecond
	_, err := h.Executar(context.Background(), ferr("POST", "/x", false, nil), nil, Identidade{})
	var ab *ErrAbortar
	if !errors.As(err, &ab) || ab.Code != "TIMEOUT" {
		t.Fatalf("err = %v", err)
	}

	srv.Close()
	_, err = h.Executar(context.Background(), ferr("POST", "/x", false, nil), nil, Identidade{})
	if !errors.As(err, &ab) || ab.Code != "REDE" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("erro vaza URL: %v", err)
	}
}

func TestParaLLM(t *testing.T) {
	r := Resultado{Dados: json.RawMessage(`[1,2]`), Avisos: []string{"campo x ignorado"}}
	m := r.ParaLLM()
	if _, ok := m["resultado"]; !ok || m["avisos"] == nil {
		t.Errorf("m = %v", m)
	}
}
