package atendimento

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/httpapi"
	"sola-agens/internal/llm"
	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
	"sola-agens/internal/transcricao"
	"sola-agens/internal/trava/memoria"
)

const ticketAudio = "ticket-do-audio"

func mensagemAudioJSON(id, tk string, duracao int, tamanho int64) string {
	m := map[string]any{
		"versao": 1, "mensagemId": id, "recebidaEm": time.Now().UTC().Format(time.RFC3339),
		"organizacao": map[string]any{"id": "org-1", "nome": "Studio Bela"},
		"contato":     map[string]any{"telefone": "5511999990000", "nomeExibicao": "Ana W"},
		"deMim":       false,
		"conteudo":    map[string]any{"tipo": "audio", "duracaoS": duracao, "mime": "audio/ogg; codecs=opus", "tamanho": tamanho, "encaminhado": false},
		"ticket":      map[string]any{"token": tk, "expiraEm": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339)},
	}
	b, _ := json.Marshal(m)
	return string(b)
}

type ambAudio struct {
	eliza *elizaFalso
	url   string
	svc   *Servico
	st    *gravador
	fake  *llm.Fake
	tr    *transcricao.Fake
	logs  *bufSeguro
}

// subirAudio monta o agens inteiro (spec real do Eliza, HTTP real) com rajada
// de 300 ms, para texto + áudio caírem no mesmo turno.
func subirAudio(t *testing.T) *ambAudio {
	t.Helper()
	eliza := novoEliza(t)
	env := map[string]string{
		"ELIZA_BASE_URL": eliza.srv.URL, "ELIZA_ENCAMINHAMENTO_SECRET": segredo, "ELIZA_AUTOATENDIMENTO_API_TOKEN": tokenSvc,
	}
	dir := yamlRapido(t)
	b, _ := os.ReadFile(filepath.Join(dir, "eliza.yaml"))
	if err := os.WriteFile(filepath.Join(dir, "eliza.yaml"),
		[]byte(strings.Replace(string(b), "rajada_ms: 40", "rajada_ms: 300", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	real, err := sqlite.Open(filepath.Join(t.TempDir(), "agens.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { real.Close() })
	a := &ambAudio{eliza: eliza, st: &gravador{Store: real}, fake: &llm.Fake{}, tr: &transcricao.Fake{}, logs: &bufSeguro{}}
	log := slog.New(slog.NewJSONHandler(a.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	motor := &agent.Motor{Store: a.st, LLM: a.fake, Transcritor: a.tr, Log: log, Config: agent.Config{PausaHumanaH: 12}}

	fab := &Fabrica{Dir: dir, Lookup: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Cliente: eliza.srv.Client(), Timeout: 2 * time.Second, Modelo: "gemini-2.5-flash", Log: log}
	conj, err := fab.Montar(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.svc = Novo(Opcoes{Conjunto: conj, Fabrica: fab, Runner: motor, Trava: &memoria.Trava{}, MaxConversas: 100, Log: log})
	api := httpapi.New(conj.Registro, a.svc, log)
	api.Eventos = a.st
	srv := httptest.NewServer(api.Routes())
	t.Cleanup(srv.Close)
	t.Cleanup(func() { a.svc.Close(5 * time.Second) })
	a.url = srv.URL
	return a
}

func corpoMidia(bytes string) string {
	return `{"data":{"mime":"audio/ogg; codecs=opus","tamanho":` + "9" + `,"base64":"` + base64.StdEncoding.EncodeToString([]byte(bytes)) + `"}}`
}

func (a *ambAudio) enviadas() []string {
	var out []string
	for _, p := range a.eliza.achar("POST", prefixo+"/mensagens") {
		var m map[string]any
		json.Unmarshal(p.Corpo, &m)
		out = append(out, m["texto"].(string))
	}
	return out
}

func TestAudioPontaAPonta(t *testing.T) {
	a := subirAudio(t)
	a.eliza.midia = func(id string) (int, string) { return 200, corpoMidia("opus-de-" + id) }
	a.tr.Textos = []string{"quero cortar o cabelo sexta"}
	a.fake.Respostas = []llm.GenerateResponse{llm.TextoResp("Claro! Que horas?")}

	// rajada: texto (ticket A) + áudio (ticket B) → um turno
	postar(t, a.url, mensagemJSON("wa-in-1", "oi", false))
	postar(t, a.url, mensagemAudioJSON("wa-au-1", ticketAudio, 10, 9))
	espera(t, "resposta enviada", func() bool { return len(a.eliza.achar("POST", prefixo+"/mensagens")) == 1 })

	midias := a.eliza.achar("GET", prefixo+"/mensagens/wa-au-1/midia")
	if len(midias) != 1 {
		t.Fatalf("GET midia = %d", len(midias))
	}
	if midias[0].Header.Get("X-Autoatendimento-Ticket") != ticketAudio || midias[0].Header.Get("Authorization") != "Bearer "+tokenSvc {
		t.Errorf("headers da mídia = %v", midias[0].Header)
	}
	if len(a.tr.Chamadas) != 1 || a.tr.Chamadas[0].Mimetype != "audio/ogg" || string(a.tr.Chamadas[0].Audio) != "opus-de-wa-au-1" {
		t.Errorf("transcrição = %+v", a.tr.Chamadas)
	}
	// o contexto usa o ticket mais recente (o do áudio)
	ctxs := a.eliza.achar("GET", prefixo+"/contexto")
	if len(ctxs) != 1 || ctxs[0].Header.Get("X-Autoatendimento-Ticket") != ticketAudio {
		t.Errorf("contexto = %+v", ctxs)
	}
	if len(a.fake.Reqs) != 1 {
		t.Fatalf("chamadas ao LLM = %d", len(a.fake.Reqs))
	}
	if got := a.fake.Reqs[0].Contents[len(a.fake.Reqs[0].Contents)-1].Parts[0].Text; got != "oi\nquero cortar o cabelo sexta" {
		t.Errorf("turno do cliente = %q", got)
	}
	espera(t, "turno registrado", func() bool { return len(a.st.modos()) == 1 })
	conv := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990000"}
	mem, _ := a.st.Janela(context.Background(), conv, 10, time.Unix(0, 0))
	if len(mem) != 2 || mem[0].Conteudo == nil || *mem[0].Conteudo != "oi\n[áudio 10s] quero cortar o cabelo sexta" {
		t.Errorf("memória = %+v", mem)
	}

	for _, proibido := range []string{"quero cortar", "oi\\n", "opus-de-", base64.StdEncoding.EncodeToString([]byte("opus-de-wa-au-1")),
		ticketAudio, ticket, tokenSvc, "5511999990000"} {
		if strings.Contains(a.logs.String(), proibido) {
			t.Errorf("o log contém %q", proibido)
		}
	}
}

func TestAudioErrosDoSistemaPontaAPonta(t *testing.T) {
	cenarios := []struct {
		nome      string
		status    int
		corpo     string
		quer      string
		escalou   bool
		modo      string
		resultado string
	}{
		{"NOT_FOUND", 404, `{"error":{"code":"NOT_FOUND","message":"x"}}`, "O áudio não chegou aqui, pode mandar de novo?", false, store.ModoFixa, agent.ResultadoOK},
		{"MEDIA_TOO_LARGE", 413, `{"error":{"code":"MEDIA_TOO_LARGE","message":"x"}}`, "Esse áudio ficou longo pra mim. Pode mandar um mais curto ou escrever?", false, store.ModoFixa, agent.ResultadoOK},
		{"502", 502, `{"error":{"code":"WHATSAPP_UNAVAILABLE","message":"x"}}`, "Não consegui resolver agora. Já avisei a equipe, que vai te responder por aqui.", true, store.ModoErro, agent.ResultadoErroSistema},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			a := subirAudio(t)
			a.eliza.midia = func(string) (int, string) { return c.status, c.corpo }
			postar(t, a.url, mensagemAudioJSON("wa-au-1", ticketAudio, 10, 9))
			espera(t, "resposta enviada", func() bool { return len(a.eliza.achar("POST", prefixo+"/mensagens")) == 1 })
			espera(t, "turno registrado", func() bool { return len(a.st.modos()) == 1 })

			if got := a.enviadas(); len(got) != 1 || got[0] != c.quer {
				t.Errorf("enviadas = %q, quer %q", got, c.quer)
			}
			if n := len(a.eliza.achar("POST", prefixo+"/escalonamentos")); (n > 0) != c.escalou {
				t.Errorf("escalonamentos = %d, escalou=%v", n, c.escalou)
			}
			a.st.mu.Lock()
			turno := a.st.turnos[0]
			a.st.mu.Unlock()
			if turno.Modo != c.modo || turno.Resultado != c.resultado {
				t.Errorf("turno = %+v", turno)
			}
			if len(a.fake.Reqs) != 0 || len(a.tr.Chamadas) != 0 {
				t.Error("sem LLM nem transcrição")
			}
		})
	}
}
