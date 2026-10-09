package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
)

func abrirStore(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "agens.db"), func() time.Time { return agora })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestDedupe(t *testing.T) {
	s, h, buf := montar(t)
	s.Eventos = abrirStore(t)
	ok := corpoJSON(false, true)
	ts := agora.Unix()

	if rec := enviar(s, rota, ok, ts, assinar(ok, ts)); rec.Code != 202 || len(h.msgs) != 1 {
		t.Fatalf("primeira: status=%d msgs=%d", rec.Code, len(h.msgs))
	}
	rec := enviar(s, rota, ok, ts, assinar(ok, ts))
	if rec.Code != 202 || len(h.msgs) != 1 {
		t.Errorf("duplicada: status=%d msgs=%d (não deveria entregar de novo)", rec.Code, len(h.msgs))
	}
	if !strings.Contains(buf.String(), `"msg":"duplicado"`) {
		t.Error("esperava log duplicado")
	}
}

func TestFilaCheiaDesmarcaEvento(t *testing.T) {
	s, h, _ := montar(t)
	s.Eventos = abrirStore(t)
	ok := corpoJSON(false, true)
	ts := agora.Unix()

	h.err = ErrFilaCheia
	if rec := enviar(s, rota, ok, ts, assinar(ok, ts)); rec.Code != 503 {
		t.Fatalf("status = %d, quer 503", rec.Code)
	}
	// o reenvio não pode virar duplicado
	h.err = nil
	if rec := enviar(s, rota, ok, ts, assinar(ok, ts)); rec.Code != 202 || len(h.msgs) != 2 {
		t.Errorf("reenvio: status=%d chamadas=%d (a 1ª já contou no fake)", rec.Code, len(h.msgs))
	}
	// agora sim é duplicada
	n := len(h.msgs)
	enviar(s, rota, ok, ts, assinar(ok, ts))
	if len(h.msgs) != n {
		t.Error("terceira deveria ser duplicada")
	}
}

// eventosQuebrado devolve erro no MarcarEvento.
type eventosQuebrado struct{ Eventos }

func (eventosQuebrado) MarcarEvento(context.Context, string, string) (bool, error) {
	return false, errors.New("banco fora")
}

func TestDedupeComErroNoStore(t *testing.T) {
	s, h, _ := montar(t)
	s.Eventos = eventosQuebrado{}
	ok := corpoJSON(false, true)
	ts := agora.Unix()
	if rec := enviar(s, rota, ok, ts, assinar(ok, ts)); rec.Code != 500 || len(h.msgs) != 0 {
		t.Errorf("status=%d msgs=%d", rec.Code, len(h.msgs))
	}
}

// --- admin ---

const tokenAdmin = "token-de-admin-secreto"

func apagar(s *Server, path, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("DELETE", path, nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	return rec
}

func TestAdminApagarContato(t *testing.T) {
	st := abrirStore(t)
	s, _, buf := montar(t)
	s.AdminToken, s.Apagador = tokenAdmin, st
	ctx := context.Background()
	id := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: telefone}
	texto := "oi"
	_ = st.AnexarMensagem(ctx, id, store.MensagemMemoria{Papel: store.PapelCliente, Conteudo: &texto, CriadaEm: agora})
	_ = st.Pausar(ctx, id, agora.Add(time.Hour))

	caminho := "/admin/agentes/eliza/tenants/org-1/contatos/" + telefone
	cases := []struct {
		nome, path, auth string
		want             int
	}{
		{"sem token", caminho, "", 401},
		{"token errado", caminho, "Bearer errado", 401},
		{"sem esquema Bearer", caminho, tokenAdmin, 401},
		{"agente desconhecido", "/admin/agentes/nada/tenants/org-1/contatos/" + telefone, "Bearer " + tokenAdmin, 404},
		{"ok", caminho, "Bearer " + tokenAdmin, 204},
		{"idempotente", caminho, "Bearer " + tokenAdmin, 204},
		{"contato que nunca existiu", "/admin/agentes/eliza/tenants/org-1/contatos/0000", "Bearer " + tokenAdmin, 204},
	}
	for _, c := range cases {
		if rec := apagar(s, c.path, c.auth); rec.Code != c.want {
			t.Errorf("%s: status = %d, quer %d", c.nome, rec.Code, c.want)
		}
	}
	if got, _ := st.Janela(ctx, id, 10, time.Unix(0, 0)); len(got) != 0 {
		t.Errorf("mensagens sobraram: %d", len(got))
	}
	if c, _ := st.Conversa(ctx, id); c.PausadaAte != nil {
		t.Error("pausa sobrou")
	}

	log := buf.String()
	for _, proibido := range []string{telefone, tokenAdmin, "errado"} {
		if strings.Contains(log, proibido) {
			t.Errorf("log contém %q", proibido)
		}
	}
	if !strings.Contains(log, mensagem.HashContato(telefone)) {
		t.Error("log deveria ter o hash do contato")
	}
}

func TestAdminSoComToken(t *testing.T) {
	s, _, _ := montar(t) // sem AdminToken: rota não existe
	rec := apagar(s, "/admin/agentes/eliza/tenants/org-1/contatos/"+telefone, "Bearer "+tokenAdmin)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("sem token configurado: status = %d, quer 404/405", rec.Code)
	}
	// token configurado vazio nunca autentica
	s.AdminToken, s.Apagador = "", abrirStore(t)
	if rec := apagar(s, "/admin/agentes/eliza/tenants/org-1/contatos/"+telefone, "Bearer "); rec.Code == 204 {
		t.Error("token vazio não pode autenticar")
	}
}
