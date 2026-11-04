package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
	"sola-agens/internal/llm"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
)

const (
	servicoUUID = "5e1a0000-0000-4000-8000-000000000001"
	contatoWeb  = "web:22222222-2222-4222-8222-222222222222"
)

var passadoDistante = time.Unix(0, 0)

// novoAmbWeb monta o agente com listarServicos como única ferramenta pública.
func novoAmbWeb(t *testing.T, respostas ...llm.GenerateResponse) *amb {
	t.Helper()
	a := novoAmb(t)
	a.sis.Ctx = contextoDeTeste()
	a.sis.Ctx.Identificacao = sistema.Identificacao{Situacao: sistema.SituacaoVisitante}
	a.reg.Web = &agentes.Web{TetoTurnosDiaOrg: 300, TurnoTimeoutS: 40}
	tools := ferramentasDeTeste()
	tools[0].Publica = true
	a.ag = NovoAgente(a.reg, tools, a.ex, a.sis, "gemini-3.6-flash")
	a.llm.Respostas = respostas
	return a
}

func msgWeb(texto string) entrada.MensagemWeb {
	return entrada.MensagemWeb{Versao: 1, Canal: "web", MensagemID: "m1", OrgID: "org1", Contato: contatoWeb, Texto: texto,
		Ticket: &entrada.Ticket{Token: "tk-visitante"}, Formulario: entrada.FormularioWeb{Etapa: 1}}
}

func (a *amb) web(texto string) (entrada.RespostaWeb, error) {
	return a.motor.TurnoWeb(context.Background(), a.ag, msgWeb(texto))
}

func TestWebSoOfereceFerramentasPublicas(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("Oi!"))
	if _, err := a.web("oi"); err != nil {
		t.Fatal(err)
	}
	var nomes []string
	for _, d := range a.llm.Reqs[0].Tools {
		nomes = append(nomes, d.Name)
	}
	if got := strings.Join(nomes, ","); got != "preencher_agendamento,listarServicos" {
		t.Fatalf("ferramentas: %s", got)
	}
}

// O modelo não abre a fronteira chamando uma ferramenta que não foi oferecida.
func TestWebRecusaEscritaMesmoSeOModeloChamar(t *testing.T) {
	a := novoAmbWeb(t,
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "2026-10-16T15:00"}),
		llm.CallResp("chamar_humano", map[string]any{"motivo": "outro", "resumo": "x"}),
		llm.TextoResp("Não consigo agendar por aqui."))
	if _, err := a.web("agenda pra mim"); err != nil {
		t.Fatal(err)
	}
	if n := a.ex.nomes(); len(n) != 0 {
		t.Fatalf("executou %v", n)
	}
	if len(a.sis.Escaladas) != 0 {
		t.Fatalf("escalou: %+v", a.sis.Escaladas)
	}
}

func TestWebDevolveAcaoSomandoAsChamadas(t *testing.T) {
	a := novoAmbWeb(t,
		llm.CallResp(nomePreencher, map[string]any{"servicoId": servicoUUID}),
		llm.CallResp(nomePreencher, map[string]any{"data": "2026-10-16", "hora": "15:00"}),
		llm.TextoResp("Preenchi."))
	r, err := a.web("corte sexta 15h")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Acoes) != 1 {
		t.Fatalf("ações: %+v", r.Acoes)
	}
	acao := r.Acoes[0]
	if acao.Tipo != "preencher_agendamento" || acao.ServicoID != servicoUUID || acao.Data != "2026-10-16" || acao.Hora != "15:00" {
		t.Fatalf("ação: %+v", acao)
	}
}

// Campo fora de formato derrubaria a resposta inteira no Zod do Eliza.
func TestWebRecusaAcaoForaDeFormato(t *testing.T) {
	casos := map[string]map[string]any{
		"id inventado":     {"servicoId": "corte"},
		"data sem sentido": {"data": "2026-99-99"},
		"hora ruim":        {"hora": "25:00"},
		"vazia":            {},
	}
	for nome, args := range casos {
		t.Run(nome, func(t *testing.T) {
			a := novoAmbWeb(t, llm.CallResp(nomePreencher, args), llm.TextoResp("Certo."))
			r, err := a.web("oi")
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Acoes) != 0 {
				t.Fatalf("ação passou: %+v", r.Acoes)
			}
			if ok, _ := a.llm.Reqs[1].Contents[len(a.llm.Reqs[1].Contents)-1].Parts[0].FunctionResponse.Response["ok"].(bool); ok {
				t.Fatal("o modelo não foi avisado")
			}
		})
	}
}

func TestWebSemAcaoSaiListaVazia(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("Oi!"))
	r, err := a.web("oi")
	if err != nil || r.Acoes == nil || len(r.Acoes) != 0 {
		t.Fatalf("%+v err=%v", r, err)
	}
}

func TestWebMascaraDocumentoAntesDoLLMEDaBase(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("Não precisa disso aqui."))
	if _, err := a.web("meu cpf é 123.456.789-09 e meu fone (11) 98765-4321"); err != nil {
		t.Fatal(err)
	}
	enviado := a.llm.Reqs[0].Contents[0].Parts[0].Text
	for _, vazou := range []string{"123.456", "98765"} {
		if strings.Contains(enviado, vazou) {
			t.Fatalf("foi ao LLM: %s", enviado)
		}
	}
	id := store.ConversaID{Agente: "eliza", Tenant: "org1", Contato: contatoWeb}
	j, err := a.st.Janela(context.Background(), id, 10, passadoDistante)
	if err != nil || len(j) != 2 {
		t.Fatalf("janela: %d err=%v", len(j), err)
	}
	if strings.Contains(*j[0].Conteudo, "123.456") || !strings.Contains(*j[0].Conteudo, "[documento]") {
		t.Fatalf("na base: %s", *j[0].Conteudo)
	}
}

func TestMascarar(t *testing.T) {
	casos := map[string]string{
		"12345678909":             "[documento]",
		"123.456.789-09":          "[documento]",
		"11987654321":             "[documento]",
		"(11) 98765-4321":         "[telefone]",
		"11 3456-7890":            "[telefone]",
		"sexta às 15h":            "sexta às 15h",
		"2026-10-16":              "2026-10-16",
		"sai R$ 150,00 por 45min": "sai R$ 150,00 por 45min",
	}
	for entrada, quer := range casos {
		if got := Mascarar(entrada); got != quer {
			t.Errorf("Mascarar(%q) = %q, quer %q", entrada, got, quer)
		}
	}
}

func TestWebRecusaContextoQueNaoEDeVisitante(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("Oi!"))
	a.sis.Ctx.Identificacao.Situacao = sistema.SituacaoIdentificado
	if _, err := a.web("oi"); !errors.Is(err, ErrWebIndisponivel) {
		t.Fatalf("erro: %v", err)
	}
	if len(a.llm.Reqs) != 0 {
		t.Fatal("chamou o LLM com identidade errada")
	}
}

func TestWebFalhaNaoGravaMensagem(t *testing.T) {
	a := novoAmbWeb(t)
	a.llm.Err = llm.ErrLLMIndisponivel
	if _, err := a.web("oi"); !errors.Is(err, ErrWebIndisponivel) {
		t.Fatalf("erro: %v", err)
	}
	id := store.ConversaID{Agente: "eliza", Tenant: "org1", Contato: contatoWeb}
	if j, _ := a.st.Janela(context.Background(), id, 10, passadoDistante); len(j) != 0 {
		t.Fatalf("gravou: %d", len(j))
	}
	if a.st.unico(t).Resultado != ResultadoErroLLM {
		t.Error("turno de erro não registrado")
	}
}

func TestWebTetoDiario(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("Oi!"), llm.TextoResp("Oi de novo!"))
	a.reg.Web.TetoTurnosDiaOrg = 1
	a.ag = NovoAgente(a.reg, a.ag.Ferramentas, a.ex, a.sis, "gemini-3.6-flash")
	if _, err := a.web("oi"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.web("oi"); !errors.Is(err, ErrTetoWeb) {
		t.Fatalf("segunda: %v", err)
	}
	if len(a.llm.Reqs) != 1 {
		t.Fatalf("passou do teto: %d chamadas", len(a.llm.Reqs))
	}
}

func TestWebSemMarkdownEComTeto(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("**Corte** "+strings.Repeat("x", 2100)))
	r, err := a.web("oi")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Resposta, "**") || len([]rune(r.Resposta)) != maxRespostaWeb {
		t.Fatalf("resposta com %d caracteres: %.20s", len([]rune(r.Resposta)), r.Resposta)
	}
}

func TestWebRespostaVaziaNaoQuebra(t *testing.T) {
	a := novoAmbWeb(t, llm.CallResp(nomePreencher, map[string]any{"data": "2026-10-16"}), llm.GenerateResponse{FinishReason: "STOP"})
	r, err := a.web("sexta")
	if err != nil || r.Resposta != textoPreenchido {
		t.Fatalf("%+v err=%v", r, err)
	}
	a = novoAmbWeb(t, llm.GenerateResponse{FinishReason: "STOP"})
	if r, err = a.web("oi"); err != nil || r.Resposta != textoRepetirWeb {
		t.Fatalf("%+v err=%v", r, err)
	}
}

func TestPromptWebProibeDadoPessoalEAgendar(t *testing.T) {
	c := contextoDeTeste()
	c.Identificacao = sistema.Identificacao{Situacao: sistema.SituacaoVisitante}
	s := PromptWeb(c, Padrao(), entrada.FormularioWeb{Etapa: 2})
	for _, quer := range []string{"NUNCA peça nome, telefone, CPF", "NÃO agenda", "Studio Bela", "Não atendemos feriados.", `"etapa":2`} {
		contem(t, s, quer)
	}
	if strings.Contains(s, "chamar_humano") || strings.Contains(s, "criarAgendamento") {
		t.Fatalf("prompt fala do que não existe no canal:\n%s", s)
	}
}
