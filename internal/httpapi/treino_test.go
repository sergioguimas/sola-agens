package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"sola-agens/internal/llm"
	"sola-agens/internal/store/sqlite"
	"sola-agens/internal/trava/memoria"
	"sola-agens/internal/treino"
)

const (
	segredoTreino = "segredo-de-treino-com-mais-de-32-bytes"
	corpoOwner    = `{"operador":{"tenant":"org-1","id":"u1","papel":"owner","nome":"Ana"}}`
	// Calculados fora do Go (hmac do Python) com segredoTreino e 1800000000. O
	// contrato 10 do Eliza §7 usa os mesmos vetores nos testes de lá.
	vetorPost   = "4a1b3a0095223988b879a08c0150ed2a69e31e900b18192cd3d6098a5719713c" // POST /sessoes, corpoOwner
	vetorGet    = "c974379c14cc79d83c920aadda3951d7c72635053b25f0abd37636700d099902" // GET /perfil
	vetorDelete = "41706b734ba1250b05e4d82e8027b92f5716ee6d929179fb4fa12fba69203470" // DELETE /perfil
	rotaSessoes = "/a/eliza/v1/treino/sessoes"
)

const yamlTreino = yamlTeste + `treino:
  segredo_env: TS
  header_assinatura: X-Treino-Sig
  header_timestamp: X-Treino-Ts
  papeis: [owner, admin]
`

func montarTreino(t *testing.T, yaml string, respostas ...llm.GenerateResponse) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "eliza.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"S": segredo, "U": "http://x", "T": "tok", "TS": segredoTreino}
	ags, err := agentes.Carregar(dir, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "agens.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(ags, &fakeHandler{}, slog.New(slog.DiscardHandler))
	s.Agora = func() time.Time { return agora }
	s.Treino = &treino.Servico{Store: st, LLM: &llm.Fake{Respostas: respostas}, Trava: &memoria.Trava{}}
	return s
}

// mensagemTreino é o que a assinatura cobre: método, caminho depois de
// /v1/treino e corpo (specs/06 §4).
func mensagemTreino(metodo, rota, corpo string) string {
	return metodo + "." + strings.TrimPrefix(rota, "/a/eliza/v1/treino") + "." + corpo
}

func assinarTreino(rota, corpo string) string {
	return assinarCom(segredoTreino, mensagemTreino("POST", rota, corpo), agora.Unix())
}

func pedir(s *Server, rota, corpo, sig string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", rota, strings.NewReader(corpo))
	r.Header.Set("X-Treino-Ts", strconv.FormatInt(agora.Unix(), 10))
	r.Header.Set("X-Treino-Sig", sig)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	return rec
}

func pedirAssinado(s *Server, rota, corpo string) *httptest.ResponseRecorder {
	return pedir(s, rota, corpo, assinarTreino(rota, corpo))
}

func codigoErro(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Erro struct{ Codigo string } `json:"erro"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("envelope de erro inválido: %s", rec.Body.String())
	}
	return e.Erro.Codigo
}

func abrirSessao(t *testing.T, s *Server) string {
	t.Helper()
	rec := pedirAssinado(s, rotaSessoes, corpoOwner)
	if rec.Code != 200 {
		t.Fatalf("abrir: %d %s", rec.Code, rec.Body.String())
	}
	var ab treino.Abertura
	if err := json.Unmarshal(rec.Body.Bytes(), &ab); err != nil || ab.SessaoID == "" {
		t.Fatalf("abertura: %s", rec.Body.String())
	}
	return ab.SessaoID
}

func TestVetorDeAssinaturaDoTreino(t *testing.T) {
	err := entrada.VerificarAssinatura([]byte(segredoTreino), strconv.FormatInt(agora.Unix(), 10),
		[]byte(mensagemTreino("POST", rotaSessoes, corpoOwner)), vetorPost, agora)
	if err != nil {
		t.Fatal(err)
	}
	s := montarTreino(t, yamlTreino, llm.TextoResp("Oi!"))
	if rec := pedir(s, rotaSessoes, corpoOwner, vetorPost); rec.Code != 200 {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	if rec := semCorpo(s, "GET", "/a/eliza/v1/treino/perfil", "owner", vetorGet); rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	if rec := semCorpo(s, "DELETE", "/a/eliza/v1/treino/perfil", "owner", vetorDelete); rec.Code != 200 {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTreinoDesligado(t *testing.T) {
	semBloco := montarTreino(t, yamlTeste)
	if rec := pedirAssinado(semBloco, rotaSessoes, corpoOwner); rec.Code != 404 || codigoErro(t, rec) != "TREINO_DESLIGADO" {
		t.Errorf("sem bloco: %d %s", rec.Code, rec.Body.String())
	}
	desconhecido := montarTreino(t, yamlTreino)
	rec := pedirAssinado(desconhecido, "/a/outro/v1/treino/sessoes", corpoOwner)
	if rec.Code != 404 || codigoErro(t, rec) != "TREINO_DESLIGADO" {
		t.Errorf("agente desconhecido: %d %s", rec.Code, rec.Body.String())
	}
	semServico := montarTreino(t, yamlTreino)
	semServico.Treino = nil
	if rec := pedirAssinado(semServico, rotaSessoes, corpoOwner); rec.Code != 404 {
		t.Errorf("sem serviço: %d", rec.Code)
	}
}

func TestAssinaturaDoTreino(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	// S9: o segredo do encaminhamento não abre o treino.
	if rec := pedir(s, rotaSessoes, corpoOwner, assinarCom(segredo, mensagemTreino("POST", rotaSessoes, corpoOwner), agora.Unix())); rec.Code != 401 {
		t.Errorf("segredo do encaminhamento: %d", rec.Code)
	}
	if rec := pedir(s, rotaSessoes, corpoOwner, "00"); rec.Code != 401 || codigoErro(t, rec) != "NAO_AUTORIZADO" {
		t.Errorf("assinatura errada: %d %s", rec.Code, rec.Body.String())
	}
	vencida := assinarCom(segredoTreino, mensagemTreino("POST", rotaSessoes, corpoOwner), agora.Unix()-301)
	r := httptest.NewRequest("POST", rotaSessoes, strings.NewReader(corpoOwner))
	r.Header.Set("X-Treino-Ts", strconv.FormatInt(agora.Unix()-301, 10))
	r.Header.Set("X-Treino-Sig", vencida)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Errorf("assinatura vencida: %d", rec.Code)
	}
}

func TestPapelEOperador(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	staff := strings.Replace(corpoOwner, "owner", "staff", 1)
	if rec := pedirAssinado(s, rotaSessoes, staff); rec.Code != 403 || codigoErro(t, rec) != "PAPEL_NAO_PERMITIDO" {
		t.Errorf("staff: %d %s", rec.Code, rec.Body.String())
	}
	semTenant := strings.Replace(corpoOwner, "org-1", "", 1)
	if rec := pedirAssinado(s, rotaSessoes, semTenant); rec.Code != 400 {
		t.Errorf("sem tenant: %d", rec.Code)
	}
	if rec := pedirAssinado(s, rotaSessoes, `{quebrado`); rec.Code != 400 {
		t.Errorf("json quebrado: %d", rec.Code)
	}
}

func TestFluxoDoChat(t *testing.T) {
	s := montarTreino(t, yamlTreino, llm.TextoResp(`"oi, tem horário?"`), llm.TextoResp("Certo."))
	id := abrirSessao(t, s)

	corpo := strings.Replace(corpoOwner, `}}`, `},"texto":"Tenho sim flor!","ticketVisitante":"ignorado"}`, 1)
	rec := pedirAssinado(s, rotaSessoes+"/"+id+"/mensagens", corpo)
	if rec.Code != 200 {
		t.Fatalf("mensagem: %d %s", rec.Code, rec.Body.String())
	}
	var r map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r["resposta"] != "Certo." || r["ensaiosFeitos"] != 0.0 || r["sugerirEncerrar"] != false {
		t.Errorf("corpo: %s", rec.Body.String())
	}

	rec = pedirAssinado(s, rotaSessoes+"/"+id+"/encerrar", corpoOwner)
	if rec.Code != 200 {
		t.Fatalf("encerrar: %d %s", rec.Code, rec.Body.String())
	}
	// As listas saem [] e não null: o Zod do Eliza exige array.
	for _, campo := range []string{`"versaoNova":null`, `"mudancas":[]`, `"aguardandoEvidencia":[]`, `"notasNovas":[]`} {
		if !strings.Contains(rec.Body.String(), campo) {
			t.Errorf("encerrar sem %s: %s", campo, rec.Body.String())
		}
	}
	rec = pedirAssinado(s, rotaSessoes+"/"+id+"/mensagens", corpo)
	if rec.Code != http.StatusConflict || codigoErro(t, rec) != "SESSAO_ENCERRADA" {
		t.Errorf("depois de encerrar: %d %s", rec.Code, rec.Body.String())
	}
}

func TestErrosDoChat(t *testing.T) {
	s := montarTreino(t, yamlTreino, llm.TextoResp("Oi!"))
	id := abrirSessao(t, s)
	longo := strings.Replace(corpoOwner, `}}`, `},"texto":"`+strings.Repeat("x", 1001)+`"}`, 1)
	if rec := pedirAssinado(s, rotaSessoes+"/"+id+"/mensagens", longo); rec.Code != 422 || codigoErro(t, rec) != "TEXTO_LONGO" {
		t.Errorf("texto longo: %d %s", rec.Code, rec.Body.String())
	}
	vazio := strings.Replace(corpoOwner, `}}`, `},"texto":"  "}`, 1)
	if rec := pedirAssinado(s, rotaSessoes+"/"+id+"/mensagens", vazio); rec.Code != 400 {
		t.Errorf("texto vazio: %d", rec.Code)
	}
	// Sessão de outro tenant responde 404, nunca 403 (specs/06 §4).
	outro := strings.Replace(strings.Replace(corpoOwner, "org-1", "org-2", 1), `}}`, `},"texto":"oi"}`, 1)
	if rec := pedirAssinado(s, rotaSessoes+"/"+id+"/mensagens", outro); rec.Code != 404 || codigoErro(t, rec) != "NAO_ENCONTRADO" {
		t.Errorf("outro tenant: %d %s", rec.Code, rec.Body.String())
	}
	if rec := pedirAssinado(s, rotaSessoes+"/"+id+"/encerrar", strings.Replace(corpoOwner, "org-1", "org-2", 1)); rec.Code != 404 {
		t.Errorf("encerrar de outro tenant: %d", rec.Code)
	}
	// LLM sem resposta: 503 com o envelope, sem detalhe técnico.
	corpo := strings.Replace(corpoOwner, `}}`, `},"texto":"oi"}`, 1)
	rec := pedirAssinado(s, rotaSessoes+"/"+id+"/mensagens", corpo)
	if rec.Code != 503 || codigoErro(t, rec) != "INDISPONIVEL" {
		t.Errorf("llm fora: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "llm") {
		t.Errorf("vazou detalhe: %s", rec.Body.String())
	}
}

func assinarCom(chave, corpo string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(chave))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + corpo))
	return hex.EncodeToString(mac.Sum(nil))
}

// llmLento demora mais que o WriteTimeout do servidor.
type llmLento struct{ espera time.Duration }

func (l llmLento) Generate(ctx context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
	time.Sleep(l.espera)
	r := llm.TextoResp("Oi!")
	return &r, nil
}

// O turno é síncrono e passa do WriteTimeout do servidor: sem estender o prazo
// a conexão cai antes da resposta. O recorder não exercita isso; o servidor real sim.
func TestTurnoLentoPassaDoWriteTimeout(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	s.Treino.LLM = llmLento{espera: 300 * time.Millisecond}
	srv := httptest.NewUnstartedServer(s.Routes())
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest("POST", srv.URL+rotaSessoes, strings.NewReader(corpoOwner))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Treino-Ts", strconv.FormatInt(agora.Unix(), 10))
	req.Header.Set("X-Treino-Sig", assinarTreino(rotaSessoes, corpoOwner))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("conexão caiu antes da resposta: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
