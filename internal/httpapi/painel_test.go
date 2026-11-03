package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"sola-agens/internal/llm"
)

const rotaPerfil = "/a/eliza/v1/treino/perfil"

func identidade(papel string) string { return "org-1.u1." + papel }

// semCorpo faz um pedido com a identidade nos headers, como GET e DELETE /perfil.
func semCorpo(s *Server, metodo, rota, papel, sig string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(metodo, rota, nil)
	r.Header.Set("X-Treino-Ts", strconv.FormatInt(agora.Unix(), 10))
	r.Header.Set("X-Treino-Sig", sig)
	r.Header.Set(headerTenant, "org-1")
	r.Header.Set(headerID, "u1")
	r.Header.Set(headerPapel, papel)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	return rec
}

func ler(s *Server, rota string) *httptest.ResponseRecorder {
	return semCorpo(s, "GET", rota, "owner",
		assinarCom(segredoTreino, mensagemTreino("GET", rota, identidade("owner")), agora.Unix()))
}

// A assinatura vale para um método e um caminho: capturar a de um GET não
// permite zerar o perfil (specs/06 S32).
func TestAssinaturaNaoValeEmOutraRota(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	if rec := semCorpo(s, "DELETE", rotaPerfil, "owner", vetorGet); rec.Code != 401 {
		t.Fatalf("assinatura de GET em DELETE: %d", rec.Code)
	}
	if rec := semCorpo(s, "GET", rotaPerfil+"/versoes", "owner", vetorGet); rec.Code != 401 {
		t.Fatalf("assinatura de /perfil em /perfil/versoes: %d", rec.Code)
	}
	// Corpo igual, rota diferente: a assinatura de abrir sessão não encerra.
	if rec := pedir(s, rotaSessoes+"/x/encerrar", corpoOwner, vetorPost); rec.Code != 401 {
		t.Fatalf("assinatura de /sessoes em /encerrar: %d", rec.Code)
	}
	// A query faz parte do que é assinado.
	sig := assinarCom(segredoTreino, mensagemTreino("GET", "/a/eliza/v1/treino/notas?estado=pendente", identidade("owner")), agora.Unix())
	if rec := semCorpo(s, "GET", "/a/eliza/v1/treino/notas?estado=aplicada", "owner", sig); rec.Code != 401 {
		t.Fatalf("query trocada: %d", rec.Code)
	}
	if rec := semCorpo(s, "GET", "/a/eliza/v1/treino/notas?estado=pendente", "owner", sig); rec.Code != 200 {
		t.Fatalf("query certa: %d %s", rec.Code, rec.Body.String())
	}
}

// O papel vai nos headers e está assinado: trocar o header invalida.
func TestPapelNosHeadersEAssinado(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	if rec := semCorpo(s, "GET", rotaPerfil, "admin", vetorGet); rec.Code != 401 {
		t.Fatalf("header trocado: %d", rec.Code)
	}
	sig := assinarCom(segredoTreino, mensagemTreino("GET", rotaPerfil, identidade("staff")), agora.Unix())
	if rec := semCorpo(s, "GET", rotaPerfil, "staff", sig); rec.Code != 403 {
		t.Fatalf("staff: %d", rec.Code)
	}
}

func TestPerfilVazio(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	rec := ler(s, rotaPerfil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var r struct {
		Versao      int             `json:"versao"`
		Perfil      json.RawMessage `json:"perfil"`
		SecaoPrompt string          `json:"secaoPrompt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Versao != 0 || r.SecaoPrompt != "" || !strings.Contains(string(r.Perfil), `"saudacoes":[]`) {
		t.Fatalf("corpo: %s", rec.Body.String())
	}
}

func TestListasVaziasSaemComoArray(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	for rota, quer := range map[string]string{
		rotaPerfil + "/versoes":          `"versoes":[]`,
		"/a/eliza/v1/treino/observacoes": `"observacoes":[]`,
		"/a/eliza/v1/treino/notas":       `"notas":[]`,
	} {
		if rec := ler(s, rota); rec.Code != 200 || !strings.Contains(rec.Body.String(), quer) {
			t.Errorf("%s: %d %s", rota, rec.Code, rec.Body.String())
		}
	}
}

func TestErrosDoPainel(t *testing.T) {
	s := montarTreino(t, yamlTreino)
	if rec := ler(s, "/a/eliza/v1/treino/observacoes?estado=qualquer"); rec.Code != 400 {
		t.Errorf("estado inválido: %d", rec.Code)
	}
	if rec := ler(s, "/a/eliza/v1/treino/notas?estado=qualquer"); rec.Code != 400 {
		t.Errorf("estado de nota inválido: %d", rec.Code)
	}
	for _, rota := range []string{rotaPerfil + "/versoes/9/ativar", rotaPerfil + "/versoes/x/ativar",
		"/a/eliza/v1/treino/observacoes/nada/rejeitar", "/a/eliza/v1/treino/observacoes/nada/restaurar"} {
		if rec := pedirAssinado(s, rota, corpoOwner); rec.Code != 404 || codigoErro(t, rec) != "NAO_ENCONTRADO" {
			t.Errorf("%s: %d %s", rota, rec.Code, rec.Body.String())
		}
	}
	marcar := strings.Replace(corpoOwner, `}}`, `},"estado":"aplicada"}`, 1)
	if rec := pedirAssinado(s, "/a/eliza/v1/treino/notas/nada/marcar", marcar); rec.Code != 404 {
		t.Errorf("nota inexistente: %d", rec.Code)
	}
	if rec := pedirAssinado(s, "/a/eliza/v1/treino/notas/nada/marcar", corpoOwner); rec.Code != 400 {
		t.Errorf("marcar sem estado: %d", rec.Code)
	}
}

func TestRotasDoPainelDesligadas(t *testing.T) {
	s := montarTreino(t, yamlTeste, llm.TextoResp("x"))
	if rec := ler(s, rotaPerfil); rec.Code != 404 || codigoErro(t, rec) != "TREINO_DESLIGADO" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}
