package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"sola-agens/internal/llm"
)

const (
	specTeste     = "../internal/openapi/testdata/eliza.openapi.json"
	agentesTeste  = "../agentes"
	modeloTeste   = "gemini-2.5-flash"
	ticketTeste   = "tk-teste"
	baseTesteHTTP = baseAPI
)

func TestCenariosEstrutura(t *testing.T) {
	cs, err := CarregarArquivo("")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int]bool{}
	for _, c := range cs {
		ids[c.ID] = true
	}
	for id := 1; id <= 15; id++ {
		if !ids[id] {
			t.Errorf("falta o cenário %d", id)
		}
	}
	if !ids[23] {
		t.Error("falta o cenário 23")
	}
	for _, fora := range []int{16, 17, 18, 19, 20, 21, 22, 24} {
		if ids[fora] {
			t.Errorf("cenário %d deveria ficar de fora (infraestrutura)", fora)
		}
	}
}

func TestValidarCenariosRecusa(t *testing.T) {
	casos := map[string]string{
		"campo desconhecido":    "- {id: 1, nome: x, cliente: {situacao: ambiguo}, rodadas: [{mensagens: [oi]}], foo: 1}",
		"situacao ruim":         "- {id: 1, nome: x, cliente: {situacao: vip}, rodadas: [{mensagens: [oi]}]}",
		"identificado sem nome": "- {id: 1, nome: x, cliente: {situacao: identificado}, rodadas: [{mensagens: [oi]}]}",
		"sem rodadas":           "- {id: 1, nome: x, cliente: {situacao: ambiguo}}",
		"id repetido": "- {id: 1, nome: x, cliente: {situacao: ambiguo}, rodadas: [{mensagens: [oi]}]}\n" +
			"- {id: 1, nome: y, cliente: {situacao: ambiguo}, rodadas: [{mensagens: [oi]}]}",
		"motivo ruim": "- {id: 1, nome: x, cliente: {situacao: ambiguo}, rodadas: [{mensagens: [oi], esperar: {escalou_com: xis}}]}",
		"chamada e proibida": "- {id: 1, nome: x, cliente: {situacao: ambiguo}, rodadas: [{mensagens: [oi], esperar: " +
			"{ferramentas_chamadas: [a], ferramentas_proibidas: [a]}}]}",
		"ref inexistente": "- {id: 1, nome: x, cliente: {situacao: ambiguo}, rodadas: [{mensagens: [oi]}], estado_final: {agendamentos: [{ref: zz}]}}",
	}
	for nome, y := range casos {
		if _, err := CarregarCenarios([]byte(y)); err == nil {
			t.Errorf("%s: esperava erro", nome)
		}
	}
}

func TestContemAlgum(t *testing.T) {
	casos := []struct {
		texto, padrao string
		quer          bool
	}{
		{"Seu horário ficou PENDENTE", "aprova|pendente", true},
		{"Confirmação feita", "confirmacao", true},
		{"nada a ver", "aprova|pendente", false},
		{"qual deles?", "qual|quais", true},
	}
	for _, c := range casos {
		if got := contemAlgum(c.texto, c.padrao); got != c.quer {
			t.Errorf("contemAlgum(%q, %q) = %v", c.texto, c.padrao, got)
		}
	}
}

func preparar(t *testing.T, f *llm.Fake) *Bancada {
	t.Helper()
	b, err := Preparar(context.Background(), Opcoes{Provedor: f, Modelo: modeloTeste, SpecPath: specTeste, AgentesDir: agentesTeste})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Fechar)
	return b
}

func cenarioPorID(t *testing.T, id int) Cenario {
	t.Helper()
	cs, err := CarregarArquivo("")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("cenário %d não existe", id)
	return Cenario{}
}

// O fastpath não chama o LLM: o cenário 4 passa com um Fake vazio.
func TestBancadaFastpathSemLLM(t *testing.T) {
	f := &llm.Fake{}
	b := preparar(t, f)
	r := b.Rodar(context.Background(), cenarioPorID(t, 4), 1)
	if !r.Passou() {
		t.Fatalf("falhas: %v", r.Falhas)
	}
	if len(f.Reqs) != 0 {
		t.Errorf("LLM chamado %d vezes", len(f.Reqs))
	}
}

// Cenário 13 roteirizado: chamar_humano → texto. O simulado grava o escalonamento.
func TestBancadaEscalaComFake(t *testing.T) {
	f := &llm.Fake{Respostas: []llm.GenerateResponse{
		llm.CallResp("chamar_humano", map[string]any{"motivo": "pedido_do_cliente", "resumo": "quer falar com uma pessoa"}),
		llm.TextoResp("Claro, avisei a equipe. Alguém te responde por aqui."),
	}}
	b := preparar(t, f)
	r := b.Rodar(context.Background(), cenarioPorID(t, 13), 1)
	if !r.Passou() {
		t.Fatalf("falhas: %v", r.Falhas)
	}
	if len(r.Ferramentas) != 1 || r.Ferramentas[0] != "chamar_humano" {
		t.Errorf("ferramentas = %v", r.Ferramentas)
	}
}

// Cenário 13 com o modelo errando (só texto): o harness tem de reprovar.
func TestBancadaReprovaQuandoModeloErra(t *testing.T) {
	f := &llm.Fake{Respostas: []llm.GenerateResponse{llm.TextoResp("Não sei.")}}
	b := preparar(t, f)
	r := b.Rodar(context.Background(), cenarioPorID(t, 13), 1)
	if r.Passou() {
		t.Fatal("esperava falha")
	}
	juntas := strings.Join(r.Falhas, "\n")
	for _, quer := range []string{"não chamou chamar_humano", "não escalou com pedido_do_cliente"} {
		if !strings.Contains(juntas, quer) {
			t.Errorf("falhas sem %q:\n%s", quer, juntas)
		}
	}
}

// Cenário 1 de ponta a ponta com o Fake: ferramentas reais no simulado, estado final conferido.
func TestBancadaAgendaComFake(t *testing.T) {
	f := &llm.Fake{}
	b := preparar(t, f)
	c := cenarioPorID(t, 1)
	// O Fake precisa dos ids do catálogo, que são fixos no simulado.
	f.Respostas = []llm.GenerateResponse{
		// rodada 1
		llm.CallResp("listarServicos", nil),
		llm.CallResp("consultarHorarios", map[string]any{"servicoId": catalogoServicos[0].ID, "data": "2026-10-15"}),
		llm.TextoResp("Amanhã tenho 14h, 15h e 16h. Qual prefere?"),
		// rodada 2
		llm.CallResp("criarAgendamento", map[string]any{"servicoId": catalogoServicos[0].ID,
			"profissionalId": catalogoProfs[0].ID, "inicio": "2026-10-15T15:00"}),
		llm.TextoResp("Pronto! Seu corte de amanhã às 15h ficou pendente, aguardando a aprovação do estúdio."),
	}
	r := b.Rodar(context.Background(), c, 1)
	if !r.Passou() {
		t.Fatalf("falhas: %v", r.Falhas)
	}
}

// O simulado recusa o que o Eliza recusa: horário fora da grade, campo inventado, sem ticket.
func TestElizaSimulado(t *testing.T) {
	spec := []byte(`{}`)
	e := NovoEliza(spec)
	defer e.Fechar()
	e.Novo(cenarioPorID(t, 1), ticketTeste)

	chamar := func(metodo, caminho, corpo, ticket string) (int, map[string]any) {
		req, _ := http.NewRequest(metodo, e.URL()+baseTesteHTTP+caminho, strings.NewReader(corpo))
		req.Header.Set("Authorization", "Bearer "+tokenServico)
		req.Header.Set("X-Autoatendimento-Versao", "1")
		if ticket != "" {
			req.Header.Set("X-Autoatendimento-Ticket", ticket)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.NewDecoder(bytes.NewReader(raw)).Decode(&m)
		return resp.StatusCode, m
	}
	codigo := func(m map[string]any) string {
		if er, ok := m["error"].(map[string]any); ok {
			s, _ := er["code"].(string)
			return s
		}
		return ""
	}
	sv, pf := catalogoServicos[0].ID, catalogoProfs[0].ID

	if s, m := chamar("GET", "/servicos", "", ""); s != 401 || codigo(m) != "TICKET_INVALID" {
		t.Errorf("sem ticket: %d %v", s, m)
	}
	if s, m := chamar("POST", "/agendamentos", `{"servicoId":"`+sv+`","profissionalId":"`+pf+`","inicio":"2026-10-15T09:30"}`, ticketTeste); s != 409 || codigo(m) != "SLOT_UNAVAILABLE" {
		t.Errorf("fora da grade: %d %v", s, m)
	}
	if s, m := chamar("POST", "/agendamentos", `{"servicoId":"`+sv+`","profissionalId":"`+pf+`","inicio":"2026-10-15T15:00","telefone":"1"}`, ticketTeste); s != 422 || codigo(m) != "VALIDATION_ERROR" {
		t.Errorf("campo inventado: %d %v", s, m)
	}
	if s, m := chamar("POST", "/agendamentos", `{"servicoId":"`+sv+`","profissionalId":"`+pf+`","inicio":"2026-10-14T11:00"}`, ticketTeste); s != 422 || codigo(m) != "NOTICE_TOO_SHORT" {
		t.Errorf("antecedência: %d %v", s, m)
	}
	if s, _ := chamar("POST", "/agendamentos", `{"servicoId":"`+sv+`","profissionalId":"`+pf+`","inicio":"2026-10-15T15:00"}`, ticketTeste); s != 201 {
		t.Errorf("criar: %d", s)
	}
	// O mesmo horário agora está ocupado para a Bia.
	s, m := chamar("GET", "/horarios?servicoId="+sv+"&data=2026-10-15&profissionalId="+pf, "", ticketTeste)
	if s != 200 || strings.Contains(string(mustJSON(m)), "15:00") {
		t.Errorf("horários depois de criar: %d %v", s, m)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestSemChaveFalhaComMensagemClara(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	var out, errw bytes.Buffer
	if code := executar(context.Background(), nil, &out, &errw); code != 2 {
		t.Errorf("código = %d, quer 2", code)
	}
	if !strings.Contains(errw.String(), "GEMINI_API_KEY") {
		t.Errorf("mensagem sem GEMINI_API_KEY: %q", errw.String())
	}
}

func TestRelatorio(t *testing.T) {
	cs, err := CarregarArquivo("")
	if err != nil {
		t.Fatal(err)
	}
	rs := []Resultado{
		{Cenario: cs[0], Rep: 1, Ferramentas: []string{"listarServicos"}, TokensIn: 1000, TokensOut: 50, CustoMicro: 425},
		{Cenario: cs[1], Rep: 1, Falhas: []string{"R1: não chamou remarcarAgendamento"}},
	}
	var w bytes.Buffer
	if Relatorio(&w, rs, 1, false) {
		t.Error("Relatorio deveria devolver false com falha")
	}
	for _, quer := range []string{"passou", "FALHOU", "1/2 passaram", "não chamou remarcarAgendamento"} {
		if !strings.Contains(w.String(), quer) {
			t.Errorf("relatório sem %q:\n%s", quer, w.String())
		}
	}
}
