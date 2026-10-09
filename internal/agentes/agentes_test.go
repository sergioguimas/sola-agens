package agentes

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const yamlOK = `id: eliza
modo_identidade: encaminhado
entrada:
  segredo_env: SEG
  header_assinatura: X-Sig
  header_timestamp: X-Ts
sistema:
  base_url_env: URL
  openapi_path: /openapi.json
  token_env: TOK
  header_ticket: X-Tk
  headers_fixos:
    X-V: "1"
operacoes:
  contexto: a
  responder: b
  escalar: c
  midia: d
midia:
  audio: processar
`

func env() func(string) (string, bool) {
	m := map[string]string{"SEG": "segredo-x", "URL": "http://eliza:3000/", "TOK": "token-y"}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func gravar(t *testing.T, dir, nome, conteudo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCarregarValido(t *testing.T) {
	dir := t.TempDir()
	gravar(t, dir, "eliza.yaml", yamlOK)
	ags, err := Carregar(dir, env())
	if err != nil || len(ags) != 1 {
		t.Fatalf("err=%v n=%d", err, len(ags))
	}
	a := ags[0]
	if a.Segredo() != "segredo-x" || a.BaseURL() != "http://eliza:3000" || a.Token() != "token-y" {
		t.Errorf("valores resolvidos errados: %q %q %q", a.Segredo(), a.BaseURL(), a.Token())
	}
	if a.Limites.MaxIter != 8 || a.Limites.RajadaMs != 3000 || a.Limites.RajadaMaxMs != 8000 ||
		a.Limites.MemoriaMensagens != 20 || a.Limites.MemoriaHoras != 24 ||
		a.Limites.AudioMaxS != 120 || a.Limites.TurnoTimeoutS != 90 {
		t.Errorf("limites padrão = %+v", a.Limites)
	}
	if a.PoliticaMidia("audio") != MidiaProcessar || a.PoliticaMidia("imagem") != MidiaRespostaFixa {
		t.Error("política de mídia")
	}
}

func TestNaoVazaSegredo(t *testing.T) {
	dir := t.TempDir()
	gravar(t, dir, "eliza.yaml", yamlOK)
	ags, _ := Carregar(dir, env())
	var sb strings.Builder
	l := slog.New(slog.NewJSONHandler(&sb, nil))
	l.Info("x", "agente", ags[0], "ptr", &ags[0])
	out := sb.String() + fmt.Sprintf("%v %+v %s %v", ags[0], ags[0], ags[0], &ags[0])
	for _, s := range []string{"segredo-x", "token-y", "eliza:3000"} {
		if strings.Contains(out, s) {
			t.Errorf("vazou %q em %s", s, out)
		}
	}
}

func TestInvalidos(t *testing.T) {
	troca := func(de, para string) string { return strings.Replace(yamlOK, de, para, 1) }
	cases := []struct {
		nome, yaml, erro string
	}{
		{"id inválido", troca("id: eliza", "id: Eliza"), "id inválido"},
		{"id curto", troca("id: eliza", "id: e"), "id inválido"},
		{"modo assercao", troca("encaminhado", "assercao"), "não suportado nesta versão"},
		{"modo anonimo", troca("encaminhado", "anonimo"), "não suportado nesta versão"},
		{"modo desconhecido", troca("encaminhado", "x"), "modo_identidade inválido"},
		{"modo ausente", troca("modo_identidade: encaminhado\n", ""), "modo_identidade é obrigatório"},
		{"sem segredo_env", troca("  segredo_env: SEG\n", ""), "entrada.segredo_env"},
		{"sem header_assinatura", troca("  header_assinatura: X-Sig\n", ""), "header_assinatura"},
		{"sem header_timestamp", troca("  header_timestamp: X-Ts\n", ""), "header_timestamp"},
		{"sem base_url_env", troca("  base_url_env: URL\n", ""), "base_url_env"},
		{"sem openapi_path", troca("  openapi_path: /openapi.json\n", ""), "openapi_path"},
		{"openapi sem barra", troca("/openapi.json", "openapi.json"), "openapi_path"},
		{"sem token_env", troca("  token_env: TOK\n", ""), "token_env"},
		{"sem header_ticket", troca("  header_ticket: X-Tk\n", ""), "header_ticket"},
		{"sem op contexto", troca("  contexto: a\n", ""), "operacoes.contexto"},
		{"sem op responder", troca("  responder: b\n", ""), "operacoes.responder"},
		{"sem op escalar", troca("  escalar: c\n", ""), "operacoes.escalar"},
		{"sem op midia", troca("  midia: d\n", ""), "operacoes.midia"},
		{"env segredo ausente", troca("SEG", "NAODEF"), "NAODEF"},
		{"env url ausente", troca("URL", "NAODEF"), "NAODEF"},
		{"env token ausente", troca("TOK", "NAODEF"), "NAODEF"},
		{"política inválida", troca("audio: processar", "audio: talvez"), "política inválida"},
		{"tipo de mídia desconhecido", troca("audio: processar", "sticker: processar"), "tipo desconhecido"},
		{"campo desconhecido", yamlOK + "foo: 1\n", "foo"},
		{"limite negativo", yamlOK + "limites:\n  max_iter: -1\n", "negativo"},
		{"rajada máx < rajada", yamlOK + "limites:\n  rajada_ms: 5000\n  rajada_max_ms: 1000\n", "rajada_max_ms"},
		{"yaml quebrado", "id: [", "yaml inválido"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			dir := t.TempDir()
			gravar(t, dir, "a.yaml", c.yaml)
			_, err := Carregar(dir, env())
			if err == nil || !strings.Contains(err.Error(), c.erro) {
				t.Errorf("err = %v, quer conter %q", err, c.erro)
			}
		})
	}
}

func TestEnvVazia(t *testing.T) {
	dir := t.TempDir()
	gravar(t, dir, "a.yaml", yamlOK)
	lookup := func(k string) (string, bool) {
		if k == "SEG" {
			return "   ", true
		}
		return env()(k)
	}
	if _, err := Carregar(dir, lookup); err == nil || !strings.Contains(err.Error(), "SEG") {
		t.Errorf("err = %v", err)
	}
}

func TestIDDuplicadoEDirVazio(t *testing.T) {
	dir := t.TempDir()
	if ags, err := Carregar(dir, env()); err != nil || len(ags) != 0 {
		t.Errorf("dir vazio: %v %d", err, len(ags))
	}
	gravar(t, dir, "a.yaml", yamlOK)
	gravar(t, dir, "b.yaml", yamlOK)
	if _, err := Carregar(dir, env()); err == nil || !strings.Contains(err.Error(), "já usado") {
		t.Errorf("err = %v", err)
	}
}

// O yaml real do repositório precisa carregar com as envs do .env.example.
func TestElizaYamlDoRepo(t *testing.T) {
	m := map[string]string{"ELIZA_ENCAMINHAMENTO_SECRET": "s", "ELIZA_BASE_URL": "http://eliza:3000", "ELIZA_AUTOATENDIMENTO_API_TOKEN": "t"}
	ags, err := Carregar("../../agentes", func(k string) (string, bool) { v, ok := m[k]; return v, ok })
	if err != nil || len(ags) != 1 || ags[0].ID != "eliza" {
		t.Fatalf("err=%v ags=%v", err, ags)
	}
}
