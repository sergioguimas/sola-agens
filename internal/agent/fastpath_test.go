package agent

import (
	"reflect"
	"testing"

	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/store"
)

func TestFastpath(t *testing.T) {
	cenarios := []struct {
		nome       string
		textos     []string
		aguardando []string
		semOps     bool
		erroRegra  bool

		wantFast     bool
		wantOperacao string
		wantResposta string
	}{
		{nome: "1 aguardando + sim confirma", textos: []string{"Sim!"}, aguardando: []string{"ag1"},
			wantFast: true, wantOperacao: "confirmarAgendamento", wantResposta: "Prontinho, Ana! Seu horário está confirmado."},
		{nome: "emoji", textos: []string{"👍"}, aguardando: []string{"ag1"},
			wantFast: true, wantOperacao: "confirmarAgendamento", wantResposta: "Prontinho, Ana! Seu horário está confirmado."},
		{nome: "acento e caixa", textos: []string{"  TÁ bom. "}, aguardando: []string{"ag1"},
			wantFast: true, wantOperacao: "confirmarAgendamento", wantResposta: "Prontinho, Ana! Seu horário está confirmado."},
		{nome: "1 aguardando + cancelar cancela", textos: []string{"cancelar"}, aguardando: []string{"ag1"},
			wantFast: true, wantOperacao: "cancelarAgendamento", wantResposta: Padrao().Respostas.Cancelado},
		{nome: "0 aguardando vai ao LLM", textos: []string{"sim"}, aguardando: nil},
		{nome: "2 aguardando vai ao LLM", textos: []string{"sim"}, aguardando: []string{"ag1", "ag2"}},
		{nome: "texto fora da lista vai ao LLM", textos: []string{"sim, mas pode ser mais tarde?"}, aguardando: []string{"ag1"}},
		{nome: "nao sozinho nao cancela", textos: []string{"não"}, aguardando: []string{"ag1"}},
		{nome: "rajada vai ao LLM", textos: []string{"sim", "sim"}, aguardando: []string{"ag1"}},
		{nome: "sem operacoes do registro, desligado", textos: []string{"sim"}, aguardando: []string{"ag1"}, semOps: true},
		{nome: "erro de regra segue para o LLM", textos: []string{"sim"}, aguardando: []string{"ag1"}, erroRegra: true,
			wantOperacao: "confirmarAgendamento"},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmb(t)
			if c.semOps {
				a.reg.Operacoes.Confirmar, a.reg.Operacoes.Cancelar = "", ""
				a.montar()
			}
			a.sis.Ctx.AguardandoConfirmacao = c.aguardando
			if c.erroRegra {
				a.ex.Fn = func(openapiFerramenta, map[string]any) (conector.Resultado, error) {
					return conector.Resultado{Status: 422, ErroRegra: &conector.ErroRegra{Erro: "NOTICE_TOO_SHORT", Mensagem: "m"}}, nil
				}
			}
			if !c.wantFast {
				a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("resposta do LLM")}
			}
			tu := a.run(c.textos...)

			if c.wantFast {
				a.semLLM()
				if tu.Modo != store.ModoFastpath || tu.Iteracoes != 0 || tu.Modelo != "" || tu.CustoMicroUSD != 0 {
					t.Errorf("turno = %+v", tu)
				}
				if !reflect.DeepEqual(a.sis.Enviadas, []string{c.wantResposta}) {
					t.Errorf("enviadas = %v", a.sis.Enviadas)
				}
				if got := a.ex.Chamadas; len(got) != 1 || got[0].Nome != c.wantOperacao ||
					!reflect.DeepEqual(got[0].Args, map[string]any{"id": "ag1"}) || got[0].Ticket != "tk-1" {
					t.Errorf("chamadas = %+v", got)
				}
				return
			}
			if tu.Modo != store.ModoLLM || len(a.llm.Reqs) != 1 {
				t.Errorf("deveria ir ao LLM: turno = %+v, chamadas = %d", tu, len(a.llm.Reqs))
			}
			if c.wantOperacao != "" && (len(a.ex.Chamadas) != 1 || a.ex.Chamadas[0].Nome != c.wantOperacao) {
				t.Errorf("chamadas = %+v", a.ex.Chamadas)
			}
			if c.wantOperacao == "" && len(a.ex.Chamadas) != 0 {
				t.Errorf("fastpath não deveria chamar o sistema: %+v", a.ex.Chamadas)
			}
			// o LLM recebe a mensagem original
			req := a.llm.Reqs[0]
			if got := req.Contents[len(req.Contents)-1].Parts[0].Text; got != juntarTextos(a.turno(c.textos...).Mensagens) {
				t.Errorf("turno enviado ao LLM = %q", got)
			}
		})
	}
}

func TestFastpathUsaPalavrasEPrimeiroNomeDoSistema(t *testing.T) {
	a := novoAmb(t)
	a.sis.Ctx.AguardandoConfirmacao = []string{"ag1"}
	a.sis.Ctx.Atendente.Palavras.Confirmar = []string{"fechou"}
	a.sis.Ctx.Identificacao.Cliente.PrimeiroNome = "Beatriz"
	conf := "Beleza, {primeiroNome}, tá marcado!"
	a.sis.Ctx.Atendente.Respostas.Confirmado = &conf
	tu := a.run("Fechou")
	if tu.Modo != store.ModoFastpath || !reflect.DeepEqual(a.sis.Enviadas, []string{"Beleza, Beatriz, tá marcado!"}) {
		t.Errorf("turno = %+v, enviadas = %v", tu, a.sis.Enviadas)
	}
}

func TestFastpathSemNomeRemoveOPlaceholder(t *testing.T) {
	a := novoAmb(t)
	a.sis.Ctx.AguardandoConfirmacao = []string{"ag1"}
	a.sis.Ctx.Identificacao = sistemaIdentificacaoDesconhecida()
	a.run("sim")
	if !reflect.DeepEqual(a.sis.Enviadas, []string{"Prontinho! Seu horário está confirmado."}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
}

func TestFastpathErrAbortarVaiParaF2(t *testing.T) {
	a := novoAmb(t)
	a.sis.Ctx.AguardandoConfirmacao = []string{"ag1"}
	a.ex.Fn = func(openapiFerramenta, map[string]any) (conector.Resultado, error) {
		return conector.Resultado{}, &conector.ErrAbortar{Code: "TIMEOUT"}
	}
	tu := a.run("sim")
	a.semLLM()
	if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroSistema || !tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if !reflect.DeepEqual(a.sis.Enviadas, []string{Padrao().Respostas.FalhaTecnica}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
}
