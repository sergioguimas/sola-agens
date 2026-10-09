package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sola-agens/internal/agentes"
)

var update = flag.Bool("update", false, "regrava testdata/eliza.tools.golden.json")

func agenteEliza() agentes.Agente {
	return agentes.Agente{ID: "eliza", Operacoes: agentes.Operacoes{
		Contexto: "obterContexto", Responder: "enviarMensagem", Escalar: "escalarParaEquipe",
		Midia: "baixarMidia", Confirmar: "confirmarAgendamento", Cancelar: "cancelarAgendamento",
	}}
}

func jsonIndentado(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGoldenEliza(t *testing.T) {
	spec, err := os.ReadFile("testdata/eliza.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := Converter(spec, agenteEliza())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 12 {
		t.Fatalf("ferramentas = %d, want 12", len(tools))
	}
	got := jsonIndentado(t, tools)
	const arq = "testdata/eliza.tools.golden.json"
	if *update {
		if err := os.WriteFile(arq, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(arq)
	if err != nil {
		t.Fatalf("golden ausente (rode com -update): %v", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("golden diferente; confira o diff e rode -update se for intencional")
	}
}

// Propriedades que o golden precisa garantir (conferência estrutural, além do texto).
func TestElizaEstrutura(t *testing.T) {
	spec, _ := os.ReadFile("testdata/eliza.openapi.json")
	tools, err := Converter(spec, agenteEliza())
	if err != nil {
		t.Fatal(err)
	}
	por := map[string]Ferramenta{}
	for _, f := range tools {
		por[f.Decl.Name] = f
	}
	for _, n := range []string{"remarcarAgendamento", "cancelarAgendamento", "confirmarAgendamento"} {
		f := por[n]
		if f.Args["id"] != DestinoPath {
			t.Errorf("%s: id deve ser path, é %q", n, f.Args["id"])
		}
		req, _ := f.Decl.Parameters["required"].([]string)
		temID := false
		for _, r := range req {
			temID = temID || r == "id"
		}
		if !temID {
			t.Errorf("%s: id deve ser required", n)
		}
	}
	for _, n := range []string{"cancelarAgendamento", "confirmarAgendamento"} {
		f := por[n]
		if f.BodyObrigatorio {
			t.Errorf("%s: body não é obrigatório", n)
		}
		if req, _ := f.Decl.Parameters["required"].([]string); len(req) != 1 || req[0] != "id" {
			t.Errorf("%s: required = %v, want só id", n, req)
		}
	}
	if f := por["consultarHorarios"]; f.Args["servicoId"] != DestinoQuery || f.Metodo != "GET" {
		t.Errorf("consultarHorarios: %+v", f)
	}
	if f := por["criarAgendamento"]; !f.BodyObrigatorio || f.Args["inicio"] != DestinoBody {
		t.Errorf("criarAgendamento: %+v", f)
	}
	for _, n := range []string{"obterContexto", "enviarMensagem", "escalarParaEquipe", "baixarMidia"} {
		if _, ok := por[n]; ok {
			t.Errorf("%s do motor virou ferramenta", n)
		}
	}
}

// specMin monta uma spec pequena com as seis operações do registro mais as extras.
func specMin(extras string, motorMarcado string) []byte {
	motor := func(id string) string {
		m := ""
		if id == motorMarcado {
			m = `"x-atendente": true,`
		}
		return `{"operationId":"` + id + `",` + m + `"responses":{}}`
	}
	s := `{"openapi":"3.1.0","paths":{
	 "/m/contexto":{"get":` + motor("obterContexto") + `},
	 "/m/msg":{"post":` + motor("enviarMensagem") + `},
	 "/m/esc":{"post":` + motor("escalarParaEquipe") + `},
	 "/m/midia":{"get":` + motor("baixarMidia") + `},
	 "/m/conf":{"post":` + motor("confirmarAgendamento") + `},
	 "/m/canc":{"post":` + motor("cancelarAgendamento") + `}`
	if extras != "" {
		s += "," + extras
	}
	return []byte(s + `},"components":{"schemas":{}}}`)
}

func TestFiltraPorXAtendente(t *testing.T) {
	spec := specMin(`"/a":{"get":{"operationId":"incluida","x-atendente":true,"summary":"S","description":"D"}},
	 "/b":{"get":{"operationId":"fora"}},
	 "/c":{"get":{"operationId":"falsa","x-atendente":false}}`, "")
	tools, err := Converter(spec, agenteEliza())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Decl.Name != "incluida" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].Decl.Description != "S\n\nD" {
		t.Errorf("description = %q", tools[0].Decl.Description)
	}
	if tools[0].Decl.Parameters != nil {
		t.Errorf("sem argumentos deveria ter Parameters nil")
	}
}

func TestErrosDeConversao(t *testing.T) {
	cases := []struct {
		nome   string
		spec   []byte
		ag     func(*agentes.Agente)
		querem string
	}{
		{"operação do motor marcada", specMin("", "enviarMensagem"), nil, "enviarMensagem"},
		{"contexto marcado", specMin("", "obterContexto"), nil, "obterContexto"},
		{"operação do registro inexistente", specMin("", ""), func(a *agentes.Agente) { a.Operacoes.Midia = "naoExiste" }, "naoExiste"},
		{"fastpath inexistente", specMin("", ""), func(a *agentes.Agente) { a.Operacoes.Cancelar = "sumiu" }, "sumiu"},
		{"nome inválido", specMin(`"/a":{"get":{"operationId":"nome-ruim","x-atendente":true}}`, ""), nil, "nome-ruim"},
		{"nome reservado", specMin(`"/a":{"get":{"operationId":"chamar_humano","x-atendente":true}}`, ""), nil, "chamar_humano"},
		{"sem operationId", specMin(`"/a":{"get":{"x-atendente":true}}`, ""), nil, "sem operationId"},
		{"operationId repetido", specMin(`"/a":{"get":{"operationId":"x"},"post":{"operationId":"x"}}`, ""), nil, "repetido"},
		{"colisão path e body", specMin(`"/a/{id}":{"post":{"operationId":"col","x-atendente":true,
			"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
			"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"}}}}}}}}`, ""),
			nil, "col"},
		{"ref externa", specMin(`"/a":{"get":{"operationId":"ext","x-atendente":true,
			"parameters":[{"name":"q","in":"query","schema":{"$ref":"https://x.com/s.json#/A"}}]}}`, ""), nil, "externa"},
		{"ciclo", specMin(`"/a":{"post":{"operationId":"cic","x-atendente":true,
			"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}`, ""), nil, "ciclo"},
		{"JSON inválido", []byte(`{`), nil, "JSON"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			spec := c.spec
			if c.nome == "ciclo" {
				spec = bytes.Replace(spec, []byte(`"schemas":{}`),
					[]byte(`"schemas":{"A":{"type":"object","properties":{"b":{"$ref":"#/components/schemas/A"}}}}`), 1)
			}
			ag := agenteEliza()
			if c.ag != nil {
				c.ag(&ag)
			}
			_, err := Converter(spec, ag)
			if err == nil || !strings.Contains(err.Error(), c.querem) {
				t.Fatalf("err = %v, want contendo %q", err, c.querem)
			}
		})
	}
}

func TestSchemaConversao(t *testing.T) {
	spec := specMin(`"/a/{id}":{"post":{"operationId":"conv","x-atendente":true,
	  "parameters":[
	    {"name":"id","in":"path","required":true,"description":"Id.","schema":{"type":"string","format":"uuid"}},
	    {"name":"X-Header","in":"header","required":true,"schema":{"type":"string"}},
	    {"name":"q","in":"query","schema":{"type":"integer","minimum":1,"maximum":9}},
	    {"$ref":"#/components/parameters/Sem"}
	  ],
	  "requestBody":{"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Corpo"}}}}}}`, "")
	spec = bytes.Replace(spec, []byte(`"components":{"schemas":{}}`), []byte(`"components":{
	 "parameters":{"Sem":{"name":"sem","in":"query","schema":{"type":"string","format":"uuid"}}},
	 "schemas":{
	  "Data":{"type":"string","format":"date-time","description":"Quando."},
	  "Corpo":{"type":"object","required":["obs","quando"],"additionalProperties":false,"properties":{
	    "obs":{"type":["string","null"],"maxLength":10,"pattern":"^a","default":"x","examples":["y"]},
	    "quando":{"$ref":"#/components/schemas/Data"},
	    "motivo":{"const":"fixo","type":"string"},
	    "tags":{"type":"array","items":{"type":"string","enum":["a","b"]},"minItems":1,"maxItems":3},
	    "email":{"type":"string","format":"email"},
	    "mail2":{"type":"string","format":"email","description":"Outro."},
	    "talvez":{"anyOf":[{"type":"integer"},{"type":"null"}]},
	    "dia":{"type":"string","format":"date"}
	  }}}}`), 1)
	tools, err := Converter(spec, agenteEliza())
	if err != nil {
		t.Fatal(err)
	}
	f := tools[0]
	got := string(jsonIndentado(t, f.Decl.Parameters))
	checks := []string{
		`"description": "Id. (formato: uuid)"`,     // dica no param com description
		`"description": "Formato: uuid."`,          // dica sem description (param "sem")
		`"nullable": true`,                         // type com null
		`"minimum": 1`,                             // numérico preservado
		`"description": "Quando."`,                 // via $ref
		`"format": "date-time"`,                    // mantido
		`"format": "date"`,                         // mantido
		`"enum": [`,                                // const e enum
		`"fixo"`,                                   // const
		`"description": "Outro. (formato: email)"`, // dica com description
		`"description": "Formato: email."`,         // dica sem description
		`"maxItems": 3`,
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("faltou %s em:\n%s", c, got)
		}
	}
	for _, ruim := range []string{"maxLength", "pattern", "default", "examples", "additionalProperties", "X-Header", `"uuid"`} {
		if strings.Contains(got, ruim) {
			t.Errorf("não deveria conter %s", ruim)
		}
	}
	if f.Args["id"] != DestinoPath || f.Args["q"] != DestinoQuery || f.Args["obs"] != DestinoBody || f.Args["sem"] != DestinoQuery {
		t.Errorf("args = %v", f.Args)
	}
	if _, ok := f.Args["X-Header"]; ok {
		t.Error("header virou argumento")
	}
	req, _ := f.Decl.Parameters["required"].([]string)
	if strings.Join(req, ",") != "id,obs,quando" {
		t.Errorf("required = %v", req)
	}
}

func TestBodyOpcionalNaoTemRequired(t *testing.T) {
	spec := specMin(`"/a":{"post":{"operationId":"opc","x-atendente":true,
	 "requestBody":{"required":false,"content":{"application/json":{"schema":{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}}}}}}}`, "")
	tools, err := Converter(spec, agenteEliza())
	if err != nil {
		t.Fatal(err)
	}
	if tools[0].BodyObrigatorio {
		t.Error("BodyObrigatorio deveria ser false")
	}
	if _, ok := tools[0].Decl.Parameters["required"]; ok {
		t.Errorf("required não deveria existir: %v", tools[0].Decl.Parameters)
	}
}

func TestOperacaoDoMotor(t *testing.T) {
	spec, _ := os.ReadFile("testdata/eliza.openapi.json")
	m, c, err := OperacaoDoMotor(spec, "baixarMidia")
	if err != nil || m != "GET" || c != "/api/v1/autoatendimento/mensagens/{mensagemId}/midia" {
		t.Errorf("= %q %q %v", m, c, err)
	}
	if _, _, err := OperacaoDoMotor(spec, "nao"); err == nil {
		t.Error("esperava erro")
	}
}

func TestCarregar(t *testing.T) {
	sem := func(_ context.Context, _ time.Duration) error { return nil }
	esperas := []time.Duration{2, 4, 8, 16, 32}

	t.Run("falha, falha, sucesso", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			switch n.Add(1) {
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
			case 2:
				_, _ = w.Write([]byte("não é json"))
			default:
				_, _ = w.Write([]byte(`{"openapi":"3.1.0"}`))
			}
		}))
		defer srv.Close()
		var pausas []time.Duration
		b, err := carregar(context.Background(), srv.Client(), srv.URL, "tok", esperas,
			func(_ context.Context, d time.Duration) error { pausas = append(pausas, d); return nil })
		if err != nil || string(b) != `{"openapi":"3.1.0"}` {
			t.Fatalf("b=%s err=%v", b, err)
		}
		if n.Load() != 3 || len(pausas) != 2 || pausas[0] != 2 || pausas[1] != 4 {
			t.Errorf("chamadas=%d pausas=%v", n.Load(), pausas)
		}
	})

	t.Run("cinco falhas", func(t *testing.T) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		_, err := carregar(context.Background(), srv.Client(), srv.URL, "tok", esperas, sem)
		if err == nil {
			t.Fatal("esperava erro")
		}
		if n.Load() != 5 {
			t.Errorf("tentativas = %d, want 5", n.Load())
		}
	})
}
