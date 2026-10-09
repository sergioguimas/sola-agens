package atendimento

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/agentes"
	"sola-agens/internal/httpapi"
	"sola-agens/internal/llm"
	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
	"sola-agens/internal/trava/memoria"
)

const (
	segredo  = "segredo-de-teste"
	tokenSvc = "token-de-servico"
	ticket   = "ticket-abc"
	prefixo  = "/api/v1/autoatendimento"
)

type bufSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *bufSeguro) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
func (w *bufSeguro) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }

type pedido struct {
	Metodo, Caminho string
	Header          http.Header
	Corpo           []byte
}

// elizaFalso faz o papel do Eliza: serve a spec real e registra o que recebe.
type elizaFalso struct {
	srv      *httptest.Server
	spec     []byte
	contexto json.RawMessage
	mu       sync.Mutex
	pedidos  []pedido
}

func novoEliza(t *testing.T) *elizaFalso {
	t.Helper()
	spec, err := os.ReadFile("../openapi/testdata/eliza.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	// o `data` do exemplo "identificado" da própria spec
	var doc map[string]any
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	var no any = doc
	for _, k := range []string{"paths", prefixo + "/contexto", "get", "responses", "200", "content", "application/json",
		"examples", "identificado", "value", "data"} {
		no = no.(map[string]any)[k]
	}
	ctxJSON, _ := json.Marshal(no)

	e := &elizaFalso{spec: spec, contexto: ctxJSON}
	e.srv = httptest.NewServer(http.HandlerFunc(e.servir))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *elizaFalso) servir(w http.ResponseWriter, r *http.Request) {
	corpo, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == prefixo+"/openapi.json" {
		w.Write(e.spec)
		return
	}
	e.mu.Lock()
	e.pedidos = append(e.pedidos, pedido{r.Method, r.URL.Path, r.Header.Clone(), corpo})
	e.mu.Unlock()

	switch {
	case r.Method == "GET" && r.URL.Path == prefixo+"/contexto":
		io.WriteString(w, `{"data":`+string(e.contexto)+`}`)
	case r.Method == "POST" && r.URL.Path == prefixo+"/agendamentos":
		w.WriteHeader(201)
		io.WriteString(w, `{"data":{"id":"ag-novo","status":"pending"}}`)
	case r.Method == "POST" && r.URL.Path == prefixo+"/mensagens":
		io.WriteString(w, `{"data":{"mensagemId":"wa-out-1"}}`)
	default:
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"code":"NOT_FOUND","message":"x"}}`)
	}
}

func (e *elizaFalso) achar(metodo, caminho string) []pedido {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []pedido
	for _, p := range e.pedidos {
		if p.Metodo == metodo && p.Caminho == caminho {
			out = append(out, p)
		}
	}
	return out
}

// gravador guarda os turnos para o teste conferir o modo.
type gravador struct {
	store.Store
	mu     sync.Mutex
	turnos []store.Turno
}

func (g *gravador) RegistrarTurno(ctx context.Context, t store.Turno) error {
	g.mu.Lock()
	g.turnos = append(g.turnos, t)
	g.mu.Unlock()
	return g.Store.RegistrarTurno(ctx, t)
}

func (g *gravador) modos() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, t := range g.turnos {
		out = append(out, t.Modo)
	}
	return out
}

func espera(t *testing.T, descricao string, cond func() bool) {
	t.Helper()
	for i := 0; i < 600; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("não ocorreu a tempo: %s", descricao)
}

// yamlRapido copia agentes/eliza.yaml com a janela de rajada curta.
func yamlRapido(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../agentes/eliza.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.Replace(s, "rajada_ms: 3000", "rajada_ms: 40", 1)
	s = strings.Replace(s, "rajada_max_ms: 8000", "rajada_max_ms: 400", 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "eliza.yaml"), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mensagemJSON(id, texto string, deMim bool) string {
	m := map[string]any{
		"versao": 1, "mensagemId": id, "recebidaEm": time.Now().UTC().Format(time.RFC3339),
		"organizacao": map[string]any{"id": "org-1", "nome": "Studio Bela"},
		"contato":     map[string]any{"telefone": "5511999990000", "nomeExibicao": "Ana W"},
		"deMim":       deMim,
		"conteudo":    map[string]any{"tipo": "texto", "texto": texto},
	}
	if !deMim {
		m["ticket"] = map[string]any{"token": ticket, "expiraEm": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func postar(t *testing.T, base, corpo string) {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(segredo))
	mac.Write([]byte(ts + "." + corpo))
	req, _ := http.NewRequest("POST", base+"/a/eliza/v1/mensagens", strings.NewReader(corpo))
	req.Header.Set("X-Eliza-Timestamp", ts)
	req.Header.Set("X-Eliza-Assinatura", hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 202 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /mensagens = %d %s", resp.StatusCode, b)
	}
}

func TestPontaAPonta(t *testing.T) {
	eliza := novoEliza(t)
	env := map[string]string{
		"ELIZA_BASE_URL": eliza.srv.URL, "ELIZA_ENCAMINHAMENTO_SECRET": segredo, "ELIZA_AUTOATENDIMENTO_API_TOKEN": tokenSvc,
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	real, err := sqlite.Open(filepath.Join(t.TempDir(), "agens.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer real.Close()
	st := &gravador{Store: real}

	logs := &bufSeguro{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	fake := &llm.Fake{Respostas: []llm.GenerateResponse{
		llm.CallResp("criarAgendamento", map[string]any{
			"servicoId": "11111111-1111-4111-8111-111111111111", "profissionalId": "22222222-2222-4222-8222-222222222222",
			"inicio": "2026-10-16T10:00"}),
		llm.TextoResp("Pronto! Seu horário ficou pendente de aprovação."),
	}}
	motor := &agent.Motor{Store: st, LLM: fake, Log: log, Config: agent.Config{PausaHumanaH: 12}}

	fab := &Fabrica{Dir: yamlRapido(t), Lookup: lookup, Cliente: eliza.srv.Client(), Timeout: 2 * time.Second,
		Modelo: "gemini-2.5-flash", Log: log}
	conj, err := fab.Montar(context.Background()) // boot com a spec real do Eliza servida pelo httptest
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	svc := Novo(Opcoes{Conjunto: conj, Fabrica: fab, Runner: motor, Trava: &memoria.Trava{}, MaxConversas: 100, Log: log})
	api := httpapi.New(conj.Registro, svc, log)
	api.Eventos = st
	agens := httptest.NewServer(api.Routes())
	defer agens.Close()

	// 1) mensagem do cliente → debounce → contexto → LLM → criarAgendamento → resposta
	postar(t, agens.URL, mensagemJSON("wa-in-1", "quero cortar o cabelo sexta às 10h", false))
	espera(t, "resposta enviada ao Eliza", func() bool { return len(eliza.achar("POST", prefixo+"/mensagens")) == 1 })

	ctxs := eliza.achar("GET", prefixo+"/contexto")
	if len(ctxs) != 1 || ctxs[0].Header.Get("X-Autoatendimento-Ticket") != ticket ||
		ctxs[0].Header.Get("Authorization") != "Bearer "+tokenSvc || ctxs[0].Header.Get("X-Autoatendimento-Versao") != "1" {
		t.Fatalf("GET contexto = %+v", ctxs)
	}
	cria := eliza.achar("POST", prefixo+"/agendamentos")
	if len(cria) != 1 {
		t.Fatalf("POST agendamentos = %d", len(cria))
	}
	if cria[0].Header.Get("X-Autoatendimento-Ticket") != ticket || cria[0].Header.Get("Authorization") != "Bearer "+tokenSvc {
		t.Errorf("headers do POST = %v", cria[0].Header)
	}
	var corpo map[string]any
	json.Unmarshal(cria[0].Corpo, &corpo)
	if corpo["inicio"] != "2026-10-16T10:00" || corpo["servicoId"] == nil || corpo["profissionalId"] == nil {
		t.Errorf("corpo do POST = %s", cria[0].Corpo)
	}
	resp := eliza.achar("POST", prefixo+"/mensagens")[0]
	var rb map[string]any
	json.Unmarshal(resp.Corpo, &rb)
	if rb["texto"] != "Pronto! Seu horário ficou pendente de aprovação." || resp.Header.Get("X-Autoatendimento-Ticket") != ticket {
		t.Errorf("POST mensagens = %s %v", resp.Corpo, resp.Header)
	}

	// o LLM viu o prompt montado do contexto real e a mensagem
	if len(fake.Reqs) != 2 {
		t.Fatalf("chamadas ao LLM = %d", len(fake.Reqs))
	}
	if !strings.Contains(fake.Reqs[0].System, "REGRA DE SEGURANÇA") {
		t.Error("prompt sem a regra de segurança")
	}
	if last := fake.Reqs[0].Contents[len(fake.Reqs[0].Contents)-1]; last.Parts[0].Text != "quero cortar o cabelo sexta às 10h" {
		t.Errorf("turno do cliente = %+v", last)
	}

	// 2) eco registrado; o deMim com o MESMO mensagemId não pausa
	espera(t, "turno registrado", func() bool { return len(st.modos()) == 1 })
	if eco, _ := st.EhEco(context.Background(), "eliza", "wa-out-1"); !eco {
		t.Fatal("eco wa-out-1 não foi registrado")
	}
	conv := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990000"}
	postar(t, agens.URL, mensagemJSON("wa-out-1", "Pronto! Seu horário ficou pendente de aprovação.", true))
	espera(t, "eco ignorado", func() bool { return strings.Contains(logs.String(), `"msg":"eco"`) })
	if c, _ := st.Conversa(context.Background(), conv); c.PausadaAte != nil {
		t.Fatalf("o eco pausou a conversa até %v", c.PausadaAte)
	}

	// 3) deMim com OUTRO id (a equipe respondeu) pausa
	postar(t, agens.URL, mensagemJSON("wa-human-1", "Oi, aqui é a Bia, já te ajudo!", true))
	espera(t, "pausa", func() bool { c, _ := st.Conversa(context.Background(), conv); return c.PausadaAte != nil })

	// 4) a próxima mensagem do cliente é só gravada: sem LLM e sem nova chamada ao Eliza
	postar(t, agens.URL, mensagemJSON("wa-in-2", "alguém aí?", false))
	espera(t, "turno pausado", func() bool { return len(st.modos()) == 2 })
	if st.modos()[1] != store.ModoPausado {
		t.Errorf("modos = %v", st.modos())
	}
	if len(fake.Reqs) != 2 {
		t.Errorf("o LLM foi chamado com a conversa pausada (%d chamadas)", len(fake.Reqs))
	}
	if n := len(eliza.achar("GET", prefixo+"/contexto")); n != 1 {
		t.Errorf("contextos = %d, quer 1 (pausada não consulta o sistema)", n)
	}

	// encerramento: nada pendente
	if err := svc.Close(5 * time.Second); err != nil {
		t.Errorf("Close: %v", err)
	}

	// nada de texto, ticket nem telefone nos logs
	for _, segredo := range []string{"quero cortar", "Pronto!", "alguém aí", ticket, tokenSvc, "5511999990000"} {
		if strings.Contains(logs.String(), segredo) {
			t.Errorf("o log contém %q", segredo)
		}
	}
}

func TestRecarregarTrocaOuMantemOAnterior(t *testing.T) {
	eliza := novoEliza(t)
	env := map[string]string{
		"ELIZA_BASE_URL": eliza.srv.URL, "ELIZA_ENCAMINHAMENTO_SECRET": segredo, "ELIZA_AUTOATENDIMENTO_API_TOKEN": tokenSvc,
	}
	dir := yamlRapido(t)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	fab := &Fabrica{Dir: dir, Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Cliente: eliza.srv.Client(), Timeout: time.Second, Modelo: "m", Log: log}
	conj, err := fab.Montar(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	svc := Novo(Opcoes{Conjunto: conj, Fabrica: fab, Runner: nil, Trava: &memoria.Trava{}, MaxConversas: 10, Log: log})
	defer svc.Close(time.Second)
	var trocas int
	svc.AoTrocar = func(r []agentes.Agente) { trocas++ }

	antigo := svc.Atual()
	if err := svc.Recarregar(context.Background()); err != nil {
		t.Fatalf("recarga válida: %v", err)
	}
	if svc.Atual() == antigo || trocas != 1 {
		t.Error("a recarga válida deveria trocar o conjunto")
	}

	// yaml inválido: mantém o conjunto atual
	atual := svc.Atual()
	if err := os.WriteFile(filepath.Join(dir, "eliza.yaml"), []byte("id: [isto não é um agente"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Recarregar(context.Background()); err == nil {
		t.Fatal("yaml inválido deveria dar erro")
	}
	if svc.Atual() != atual || trocas != 1 {
		t.Error("a recarga inválida não pode trocar o conjunto")
	}
}

func TestFabricaRecusaOperacaoDoFastpathInexistente(t *testing.T) {
	eliza := novoEliza(t)
	env := map[string]string{
		"ELIZA_BASE_URL": eliza.srv.URL, "ELIZA_ENCAMINHAMENTO_SECRET": segredo, "ELIZA_AUTOATENDIMENTO_API_TOKEN": tokenSvc,
	}
	dir := yamlRapido(t)
	b, _ := os.ReadFile(filepath.Join(dir, "eliza.yaml"))
	os.WriteFile(filepath.Join(dir, "eliza.yaml"),
		[]byte(strings.Replace(string(b), "confirmar: confirmarAgendamento", "confirmar: confirmarAgendameto", 1)), 0o644)
	fab := &Fabrica{Dir: dir, Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Cliente: eliza.srv.Client(), Timeout: time.Second}
	if _, err := fab.Montar(context.Background()); err == nil || !strings.Contains(err.Error(), "confirmar") {
		t.Fatalf("err = %v", err)
	}
}
