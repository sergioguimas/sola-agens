package agent

import (
	"strings"
	"testing"

	"sola-agens/internal/llm"
	"sola-agens/internal/openapi"
	"sola-agens/internal/sistema"
)

// ferramentasComAgenda marca criarAgendamento como operação de agenda e
// listarServicos como pública (ela segue valendo com a agenda fechada).
func ferramentasComAgenda() []openapi.Ferramenta {
	tools := ferramentasDeTeste()
	tools[0].Publica = true
	tools[1].Agenda = true
	tools[1].Publica = true
	return tools
}

func nomesDasTools(r llm.GenerateRequest) string {
	var n []string
	for _, d := range r.Tools {
		n = append(n, d.Name)
	}
	return strings.Join(n, ",")
}

func contextoFechado() sistema.Contexto {
	c := contextoDeTeste()
	c.Politica.JanelaMaximaDias = 0
	return c
}

func TestPromptTriagemSoComJanelaZero(t *testing.T) {
	fechado := Prompt(contextoFechado(), Padrao(), "")
	contem(t, fechado, "MODO TRIAGEM")
	contem(t, fechado, "agenda fechada para marcação automática")
	contem(t, fechado, "chamar_humano(motivo=triagem)")
	contem(t, fechado, "Para cancelar ou confirmar")
	for _, nao := range []string{"até 0 dias", "ofereça até 3 opções", "Para marcar:", "Para cancelar, remarcar"} {
		if strings.Contains(fechado, nao) {
			t.Errorf("prompt com janela 0 não deveria conter %q", nao)
		}
	}

	aberto := Prompt(contextoDeTeste(), Padrao(), "")
	if strings.Contains(aberto, "MODO TRIAGEM") || strings.Contains(aberto, "agenda fechada") {
		t.Error("janela 30 não pode ter triagem")
	}
	contem(t, aberto, "Política: antecedência mínima 120 min, até 30 dias à frente, no máximo 3 agendamentos ativos.")
	contem(t, aberto, "Para marcar: ache o serviço")
	// a janela 1 é a menor aberta: nada de triagem.
	c := contextoDeTeste()
	c.Politica.JanelaMaximaDias = 1
	if p := Prompt(c, Padrao(), ""); strings.Contains(p, "MODO TRIAGEM") || !strings.Contains(p, "até 1 dias à frente") {
		t.Error("janela 1 deveria seguir o prompt normal")
	}
}

func TestPromptWebAgendaFechada(t *testing.T) {
	f := PromptWeb(contextoFechado(), Padrao(), msgWeb("").Formulario)
	contem(t, f, "agenda online está fechada")
	contem(t, f, "agenda fechada para marcação online")
	for _, nao := range []string{"preencher_agendamento", "até 0 dias", "Ofereça até 3 opções"} {
		if strings.Contains(f, nao) {
			t.Errorf("prompt web com janela 0 não deveria conter %q", nao)
		}
	}
	a := PromptWeb(contextoDeTeste(), Padrao(), msgWeb("").Formulario)
	contem(t, a, "preencher_agendamento")
	contem(t, a, "até 30 dias à frente")
}

func TestWhatsAppFiltraFerramentasDeAgendaComJanelaZero(t *testing.T) {
	for _, c := range []struct {
		janela int
		want   string
	}{
		{0, "listarServicos,confirmarAgendamento,cancelarAgendamento,chamar_humano"},
		{30, "listarServicos,criarAgendamento,confirmarAgendamento,cancelarAgendamento,chamar_humano"},
	} {
		a := novoAmb(t)
		a.sis.Ctx.Politica.JanelaMaximaDias = c.janela
		a.ag = NovoAgente(a.reg, ferramentasComAgenda(), a.ex, a.sis, "gemini-2.5-flash")
		a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Oi!")}
		a.run("oi")
		if got := nomesDasTools(a.llm.Reqs[0]); got != c.want {
			t.Errorf("janela %d: ferramentas = %s, quer %s", c.janela, got, c.want)
		}
	}
}

func TestWhatsAppChamadaDeAgendaComJanelaZeroEDesconhecida(t *testing.T) {
	a := novoAmb(t)
	a.sis.Ctx.Politica.JanelaMaximaDias = 0
	a.ag = NovoAgente(a.reg, ferramentasComAgenda(), a.ex, a.sis, "gemini-2.5-flash")
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "2026-10-16T15:00"}),
		llm.TextoResp("A equipe confirma."),
	}
	tu := a.run("quero marcar sexta 15h")
	if n := a.ex.nomes(); len(n) != 0 {
		t.Fatalf("executou %v", n)
	}
	fr := a.llm.Reqs[1].Contents[len(a.llm.Reqs[1].Contents)-1].Parts[0].FunctionResponse
	if fr.Response["erro"] != "FERRAMENTA_DESCONHECIDA" {
		t.Errorf("resposta = %+v", fr.Response)
	}
	if len(tu.Ferramentas) != 1 || tu.Ferramentas[0].Codigo != "FERRAMENTA_DESCONHECIDA" {
		t.Errorf("log = %+v", tu.Ferramentas)
	}

	// com janela aberta a mesma chamada executa.
	b := novoAmb(t)
	b.ag = NovoAgente(b.reg, ferramentasComAgenda(), b.ex, b.sis, "gemini-2.5-flash")
	b.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "2026-10-16T15:00"}),
		llm.TextoResp("Marquei."),
	}
	b.run("quero marcar sexta 15h")
	if n := b.ex.nomes(); len(n) != 1 || n[0] != "criarAgendamento" {
		t.Fatalf("janela 30 deveria executar: %v", n)
	}
}

func TestChamarHumanoAceitaTriagem(t *testing.T) {
	a := novoAmb(t)
	a.sis.Ctx.Politica.JanelaMaximaDias = 0
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("chamar_humano", map[string]any{"motivo": "triagem", "resumo": "Ana, corte, sexta à tarde"}),
		llm.TextoResp("A equipe confirma o horário com você."),
	}
	tu := a.run("quero cortar o cabelo sexta à tarde")
	if len(a.sis.Escaladas) != 1 || a.sis.Escaladas[0].Motivo != "triagem" {
		t.Fatalf("escaladas = %+v", a.sis.Escaladas)
	}
	if !tu.Escalou || tu.Resultado != ResultadoOK {
		t.Errorf("turno = %+v", tu)
	}
	enum := chamarHumanoDecl().Parameters["properties"].(map[string]any)["motivo"].(map[string]any)["enum"].([]any)
	achou := false
	for _, m := range enum {
		achou = achou || m == "triagem"
	}
	if !achou {
		t.Errorf("enum sem triagem: %v", enum)
	}
}

// Sistema antigo, que ainda não conhece "triagem", recusa o motivo: o agente tenta de
// novo com "fora_da_politica" e a equipe é avisada do mesmo jeito.
func TestChamarHumanoTriagemCaiParaForaDaPolitica(t *testing.T) {
	a := novoAmb(t)
	a.sis.Ctx.Politica.JanelaMaximaDias = 0
	a.sis.EscalarErrs = []error{&sistema.ErrSistema{Code: "VALIDATION_ERROR", Status: 422}}
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("chamar_humano", map[string]any{"motivo": "triagem", "resumo": "Ana, corte, sexta à tarde"}),
		llm.TextoResp("A equipe confirma o horário com você."),
	}
	tu := a.run("quero cortar o cabelo sexta à tarde")
	if len(a.sis.Escaladas) != 2 || a.sis.Escaladas[0].Motivo != "triagem" || a.sis.Escaladas[1].Motivo != "fora_da_politica" {
		t.Fatalf("escaladas = %+v", a.sis.Escaladas)
	}
	if !tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
}

func TestWebAgendaFechadaSemFerramentasDeAgendaNemPreencher(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("A agenda online está fechada."))
	a.ag = NovoAgente(a.reg, ferramentasComAgenda(), a.ex, a.sis, "gemini-2.5-flash")
	a.sis.Ctx.Politica.JanelaMaximaDias = 0
	if _, err := a.web("quero marcar"); err != nil {
		t.Fatal(err)
	}
	if got := nomesDasTools(a.llm.Reqs[0]); got != "listarServicos" {
		t.Errorf("ferramentas = %s", got)
	}

	// o modelo tenta mesmo assim: preencher e agenda valem como desconhecidas.
	b := novoAmbWeb(t,
		llm.CallResp(nomePreencher, map[string]any{"servicoId": servicoUUID}),
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "2026-10-16T15:00"}),
		llm.TextoResp("Fale com o estabelecimento."))
	b.ag = NovoAgente(b.reg, ferramentasComAgenda(), b.ex, b.sis, "gemini-2.5-flash")
	b.sis.Ctx.Politica.JanelaMaximaDias = 0
	r, err := b.web("quero marcar")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Acoes) != 0 || len(b.ex.nomes()) != 0 {
		t.Fatalf("ações %+v, executou %v", r.Acoes, b.ex.nomes())
	}
}

func TestWebJanelaAbertaMantemFerramentasDeAgenda(t *testing.T) {
	a := novoAmbWeb(t, llm.TextoResp("Oi!"))
	a.ag = NovoAgente(a.reg, ferramentasComAgenda(), a.ex, a.sis, "gemini-2.5-flash")
	if _, err := a.web("oi"); err != nil {
		t.Fatal(err)
	}
	if got := nomesDasTools(a.llm.Reqs[0]); got != "preencher_agendamento,listarServicos,criarAgendamento" {
		t.Errorf("ferramentas = %s", got)
	}
}
