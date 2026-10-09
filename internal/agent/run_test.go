package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
)

func TestFerramentaDepoisResposta(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{
		respComTokens(llm.CallResp("listarServicos", nil), 100, 10),
		respComTokens(llm.TextoResp("Temos corte e barba."), 150, 20),
	}
	tu := a.run("quais serviços?")

	if got := a.ex.nomes(); !reflect.DeepEqual(got, []string{"listarServicos"}) {
		t.Fatalf("ferramentas executadas = %v", got)
	}
	if a.ex.Chamadas[0].Ticket != "tk-1" {
		t.Errorf("ticket enviado = %q", a.ex.Chamadas[0].Ticket)
	}
	if !reflect.DeepEqual(a.sis.Enviadas, []string{"Temos corte e barba."}) {
		t.Fatalf("enviadas = %v", a.sis.Enviadas)
	}
	if eco, _ := a.st.EhEco(context.Background(), "eliza", "out-1"); !eco {
		t.Error("eco da resposta não foi registrado")
	}
	if tu.Modo != store.ModoLLM || tu.Resultado != ResultadoOK || tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if tu.Iteracoes != 2 || tu.Modelo != "gemini-2.5-flash" {
		t.Errorf("iteracoes/modelo = %d/%s", tu.Iteracoes, tu.Modelo)
	}
	if tu.TokensEntrada != 250 || tu.TokensSaida != 30 {
		t.Errorf("tokens = %d/%d", tu.TokensEntrada, tu.TokensSaida)
	}
	if want := llm.Custo("gemini-2.5-flash", 250, 30); tu.CustoMicroUSD != want || want == 0 {
		t.Errorf("custo = %d, quer %d", tu.CustoMicroUSD, want)
	}
	if want := []store.FerramentaLog{{Nome: "listarServicos", Status: 200}}; !reflect.DeepEqual(tu.Ferramentas, want) {
		t.Errorf("ferramentas do turno = %+v", tu.Ferramentas)
	}
	if !reflect.DeepEqual(tu.MensagemIDs, []string{"in-1"}) {
		t.Errorf("mensagem_ids = %v", tu.MensagemIDs)
	}
	if want := []string{"cliente:quais serviços?", "agente:Temos corte e barba."}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v", a.janela())
	}

	// requisição: modelo, tools (+ chamar_humano) e o turno do cliente
	req := a.llm.Reqs[0]
	if req.Model != "gemini-2.5-flash" || req.ThinkingBudget == nil || *req.ThinkingBudget != 0 {
		t.Errorf("req = %+v", req)
	}
	var nomes []string
	for _, d := range req.Tools {
		nomes = append(nomes, d.Name)
	}
	if want := []string{"listarServicos", "criarAgendamento", "confirmarAgendamento", "cancelarAgendamento", "chamar_humano"}; !reflect.DeepEqual(nomes, want) {
		t.Errorf("tools = %v", nomes)
	}
	contem(t, req.System, "Studio Bela")
	if ultimo := req.Contents[len(req.Contents)-1]; ultimo.Role != "user" || ultimo.Parts[0].Text != "quais serviços?" {
		t.Errorf("último turno = %+v", ultimo)
	}
}

func TestChamadasParalelasExecutamTodasERespondemTodas(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{
		{Parts: []llm.Part{chamadaLLM("listarServicos", nil), chamadaLLM("criarAgendamento", map[string]any{"inicio": "2026-10-16T10:00"})}},
		llm.TextoResp("Pronto."),
	}
	tu := a.run("marca pra sexta")
	if got := a.ex.nomes(); !reflect.DeepEqual(got, []string{"listarServicos", "criarAgendamento"}) {
		t.Fatalf("executadas = %v", got)
	}
	resp := a.llm.Reqs[1].Contents
	ultimo := resp[len(resp)-1]
	if ultimo.Role != "user" || len(ultimo.Parts) != 2 ||
		ultimo.Parts[0].FunctionResponse.Name != "listarServicos" || ultimo.Parts[1].FunctionResponse.Name != "criarAgendamento" {
		t.Fatalf("functionResponses = %+v", ultimo.Parts)
	}
	if modelo := resp[len(resp)-2]; modelo.Role != "model" || len(modelo.Parts) != 2 {
		t.Errorf("turno do modelo = %+v", modelo)
	}
	if len(tu.Ferramentas) != 2 || tu.Iteracoes != 2 {
		t.Errorf("turno = %+v", tu)
	}
}

func TestErroDeRegraVoltaAoLLMEOLoopContinua(t *testing.T) {
	a := novoAmb(t)
	a.ex.Fn = func(f openapiFerramenta, args map[string]any) (conector.Resultado, error) {
		return conector.Resultado{Status: 409, ErroRegra: &conector.ErroRegra{Erro: "SLOT_UNAVAILABLE", Mensagem: "horário ocupado",
			Detalhes: json.RawMessage(`{"sugestoes":["2026-10-16T11:00"]}`)}}, nil
	}
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "2026-10-16T10:00"}),
		llm.TextoResp("Esse horário acabou de ocupar. Pode às 11h?"),
	}
	tu := a.run("quero sexta às 10")

	fr := a.llm.Reqs[1].Contents[len(a.llm.Reqs[1].Contents)-1].Parts[0].FunctionResponse
	if fr.Response["erro"] != "SLOT_UNAVAILABLE" || fr.Response["mensagem"] != "horário ocupado" {
		t.Errorf("functionResponse = %+v", fr.Response)
	}
	if tu.Resultado != ResultadoOK || tu.Modo != store.ModoLLM || tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if want := []store.FerramentaLog{{Nome: "criarAgendamento", Status: 409, Codigo: "SLOT_UNAVAILABLE"}}; !reflect.DeepEqual(tu.Ferramentas, want) {
		t.Errorf("ferramentas = %+v", tu.Ferramentas)
	}
	if len(a.sis.Escaladas) != 0 {
		t.Error("erro de regra não escala")
	}
}

func TestFerramentaDesconhecidaVoltaAoLLM(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("inventada", nil), llm.TextoResp("ok")}
	a.run("oi")
	fr := a.llm.Reqs[1].Contents[len(a.llm.Reqs[1].Contents)-1].Parts[0].FunctionResponse
	if fr.Response["erro"] != "FERRAMENTA_DESCONHECIDA" {
		t.Errorf("functionResponse = %+v", fr.Response)
	}
	if len(a.ex.Chamadas) != 0 {
		t.Error("não deveria chamar o sistema")
	}
}

func TestErrAbortarVaiParaF2(t *testing.T) {
	a := novoAmb(t)
	a.ex.Fn = func(openapiFerramenta, map[string]any) (conector.Resultado, error) {
		return conector.Resultado{}, &conector.ErrAbortar{Code: "INTERNAL_ERROR", Status: 500, RequestID: "req-9"}
	}
	a.llm.Respostas = []llm.GenerateResponse{
		{Parts: []llm.Part{chamadaLLM("criarAgendamento", map[string]any{"inicio": "x"}), chamadaLLM("listarServicos", nil)}},
	}
	tu := a.run("marca aí")

	if !reflect.DeepEqual(a.sis.Enviadas, []string{Padrao().Respostas.FalhaTecnica}) {
		t.Fatalf("enviadas = %v", a.sis.Enviadas)
	}
	if len(a.sis.Escaladas) != 1 || a.sis.Escaladas[0].Motivo != "falha_tecnica" ||
		a.sis.Escaladas[0].Resumo != "Falha técnica no atendimento automático: ferramenta" {
		t.Fatalf("escaladas = %+v", a.sis.Escaladas)
	}
	if strings.Contains(a.sis.Escaladas[0].Resumo, "marca aí") {
		t.Error("o resumo não pode ter texto do cliente")
	}
	if got := a.ex.nomes(); len(got) != 1 {
		t.Errorf("a 2ª chamada não deveria executar depois do abort: %v", got)
	}
	if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroSistema || !tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if want := []store.FerramentaLog{{Nome: "criarAgendamento", Status: 500, Codigo: "INTERNAL_ERROR"}}; !reflect.DeepEqual(tu.Ferramentas, want) {
		t.Errorf("ferramentas = %+v", tu.Ferramentas)
	}
	if want := []string{"cliente:marca aí"}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v (só a mensagem do cliente)", a.janela())
	}
}

func TestF2EscalarComMotivoRejeitadoTentaOutro(t *testing.T) {
	a := novoAmb(t)
	a.sis.EscalarErrs = []error{&sistema.ErrSistema{Code: "VALIDATION_ERROR", Status: 422}}
	a.llm.Err = llm.ErrLLMIndisponivel
	tu := a.run("oi")
	if len(a.sis.Escaladas) != 2 || a.sis.Escaladas[0].Motivo != "falha_tecnica" || a.sis.Escaladas[1].Motivo != "outro" {
		t.Fatalf("escaladas = %+v", a.sis.Escaladas)
	}
	if tu.Resultado != ResultadoErroLLM || !tu.Escalou || tu.Modo != store.ModoErro {
		t.Errorf("turno = %+v", tu)
	}
	contem(t, a.sis.Escaladas[0].Resumo, ": llm")
}

func TestF2EnvioFalhoSoLogaEEscalaMesmoAssim(t *testing.T) {
	a := novoAmb(t)
	a.sis.EnvioErr = &conector.ErrAbortar{Code: "WHATSAPP_UNAVAILABLE", Status: 503}
	a.llm.Err = llm.ErrLLMIndisponivel
	tu := a.run("oi")
	if len(a.sis.Escaladas) != 1 || !tu.Escalou {
		t.Errorf("escaladas = %+v, turno = %+v", a.sis.Escaladas, tu)
	}
}

func TestRespostaVaziaSegundaChamadaSemToolsEFeitoAposEscrita(t *testing.T) {
	a := novoAmb(t)
	vazia := llm.GenerateResponse{FinishReason: "STOP"}
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "2026-10-16T10:00"}),
		vazia, vazia,
	}
	tu := a.run("marca sexta 10h")

	if len(a.llm.Reqs) != 3 {
		t.Fatalf("chamadas ao LLM = %d, quer 3", len(a.llm.Reqs))
	}
	if len(a.llm.Reqs[1].Tools) == 0 || len(a.llm.Reqs[2].Tools) != 0 {
		t.Errorf("tools: 2ª chamada = %d (com), 3ª = %d (sem)", len(a.llm.Reqs[1].Tools), len(a.llm.Reqs[2].Tools))
	}
	pedido := a.llm.Reqs[2].Contents[len(a.llm.Reqs[2].Contents)-1]
	if pedido.Role != "user" || pedido.Parts[0].Text != pedidoTextoFinal {
		t.Errorf("pedido do texto final = %+v", pedido)
	}
	if !reflect.DeepEqual(a.sis.Enviadas, []string{"Feito! Mais alguma coisa?"}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
	if tu.Resultado != ResultadoOK || tu.Modo != store.ModoLLM || tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
}

func TestRespostaVaziaRecuperadaNaSegundaChamada(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{{FinishReason: "STOP"}, llm.TextoResp("Oi! Como posso ajudar?")}
	a.run("oi")
	if !reflect.DeepEqual(a.sis.Enviadas, []string{"Oi! Como posso ajudar?"}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
}

func TestRespostaVaziaSemEscritaVaiParaF2(t *testing.T) {
	a := novoAmb(t)
	vazia := llm.GenerateResponse{FinishReason: "STOP"}
	a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("listarServicos", nil), vazia, vazia}
	tu := a.run("quais serviços?")
	if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroLLM || !tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if !reflect.DeepEqual(a.sis.Enviadas, []string{Padrao().Respostas.FalhaTecnica}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
}

func TestCandidatoVazioDoProvedorTambemRecupera(t *testing.T) {
	a := novoAmb(t)
	fake := &erroUmaVez{err: llm.ErrRespostaVazia, depois: llm.TextoResp("Certo!")}
	a.motor.LLM = fake
	a.run("oi")
	if !reflect.DeepEqual(a.sis.Enviadas, []string{"Certo!"}) || fake.n != 2 {
		t.Errorf("enviadas = %v, chamadas = %d", a.sis.Enviadas, fake.n)
	}
}

type erroUmaVez struct {
	err    error
	depois llm.GenerateResponse
	n      int
}

func (e *erroUmaVez) Generate(context.Context, llm.GenerateRequest) (*llm.GenerateResponse, error) {
	e.n++
	if e.n == 1 {
		return nil, e.err
	}
	r := e.depois
	return &r, nil
}

func TestMaxIterVaiParaF2(t *testing.T) {
	a := novoAmb(t)
	a.reg.Limites.MaxIter = 2
	a.montar()
	a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("listarServicos", nil), llm.CallResp("listarServicos", nil), llm.CallResp("listarServicos", nil)}
	tu := a.run("oi")
	if len(a.llm.Reqs) != 2 {
		t.Errorf("chamadas ao LLM = %d, quer 2 (max_iter)", len(a.llm.Reqs))
	}
	if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroLLM || !tu.Escalou || tu.Iteracoes != 2 {
		t.Errorf("turno = %+v", tu)
	}
	contem(t, a.sis.Escaladas[0].Resumo, "limite de iterações")
}

func TestFalhaDoLLMVaiParaF2(t *testing.T) {
	for nome, err := range map[string]error{
		"indisponivel": llm.ErrLLMIndisponivel,
		"definitivo":   &llm.ErrLLMDefinitivo{Status: 400, Msg: "x"},
		"finishReason": &llm.ErrFinishReason{Reason: "SAFETY"},
	} {
		t.Run(nome, func(t *testing.T) {
			a := novoAmb(t)
			a.llm.Err = err
			tu := a.run("oi")
			if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroLLM || !tu.Escalou {
				t.Errorf("turno = %+v", tu)
			}
		})
	}
}

// bloqueado só volta quando o contexto acaba.
type bloqueado struct{}

func (bloqueado) Generate(ctx context.Context, _ llm.GenerateRequest) (*llm.GenerateResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTimeoutDoTurnoVaiParaF2ComContextoNovoNoEnvio(t *testing.T) {
	a := novoAmb(t)
	a.motor.LLM = bloqueado{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := a.motor.Run(ctx, a.turno("oi")); err != nil {
		t.Fatal(err)
	}
	tu := a.st.unico(t)
	if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroLLM || !tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if len(a.sis.Enviadas) != 1 {
		t.Errorf("a falhaTecnica deveria sair mesmo com o prazo vencido: %v", a.sis.Enviadas)
	}
	contem(t, a.sis.Escaladas[0].Resumo, "tempo esgotado")
}

func TestTicketInvalidoNoContextoNaoRespondeNemEscala(t *testing.T) {
	for _, code := range []string{"TICKET_INVALID", "TICKET_EXPIRED", "TICKET_MISSING", "ADDON_INACTIVE", "UNAUTHORIZED", "VERSION_MISMATCH"} {
		t.Run(code, func(t *testing.T) {
			a := novoAmb(t)
			a.sis.CtxErr = &conector.ErrAbortar{Code: code, Status: 401}
			tu := a.run("oi")
			if tu.Resultado != ResultadoTicketInvalid || tu.Modo != store.ModoErro || tu.Escalou {
				t.Errorf("turno = %+v", tu)
			}
			if len(a.sis.Enviadas) != 0 || len(a.sis.Escaladas) != 0 {
				t.Errorf("não deveria responder nem escalar: %v %v", a.sis.Enviadas, a.sis.Escaladas)
			}
			a.semLLM()
			if want := []string{"cliente:oi"}; !reflect.DeepEqual(a.janela(), want) {
				t.Errorf("memória = %v", a.janela())
			}
		})
	}
}

func TestErroTecnicoNoContextoVaiParaF2(t *testing.T) {
	a := novoAmb(t)
	a.sis.CtxErr = &conector.ErrAbortar{Code: "TIMEOUT"}
	tu := a.run("oi")
	if tu.Modo != store.ModoErro || tu.Resultado != ResultadoErroSistema || !tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	contem(t, a.sis.Escaladas[0].Resumo, ": contexto")
}

func TestPausadoNaoChamaLLMNemSistema(t *testing.T) {
	a := novoAmb(t)
	if err := a.st.Pausar(context.Background(), conversaDeTeste, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tu := a.run("alguém aí?", "oi?")
	a.semLLM()
	if a.sis.CtxChamadas != 0 || len(a.sis.Enviadas) != 0 || len(a.ex.Chamadas) != 0 {
		t.Error("conversa pausada não deve chamar o sistema")
	}
	if tu.Modo != store.ModoPausado || tu.Resultado != ResultadoOK {
		t.Errorf("turno = %+v", tu)
	}
	if want := []string{"cliente:alguém aí?", "cliente:oi?"}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v", a.janela())
	}
}

func TestPausaVencidaVoltaAoNormal(t *testing.T) {
	a := novoAmb(t)
	a.st.Pausar(context.Background(), conversaDeTeste, time.Now().Add(-time.Minute))
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Olá!")}
	tu := a.run("oi")
	if tu.Modo != store.ModoLLM {
		t.Errorf("modo = %s", tu.Modo)
	}
}

func TestMidiaSemTextoRespostaFixa(t *testing.T) {
	cenarios := []struct {
		nome   string
		anexos []mensagem.Anexo
		quer   string
		mem    string
	}{
		{"imagem", []mensagem.Anexo{{Tipo: "imagem"}}, Padrao().Respostas.MidiaNaoSuportada, "[imagem]"},
		// FASE 5: áudio com política processar ainda é resposta fixa.
		{"audio", []mensagem.Anexo{{Tipo: "audio", DuracaoS: 12}}, Padrao().Respostas.MidiaNaoSuportada, "[áudio 12s]"},
		{"outro", []mensagem.Anexo{{Tipo: "outro", TipoOriginal: "sticker"}}, Padrao().Respostas.NaoSuportado, "[anexo]"},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmb(t)
			tu := a.turno()
			tu.Mensagens = []mensagem.Mensagem{{ID: "m1", Anexos: c.anexos}}
			if err := a.motor.Run(context.Background(), tu); err != nil {
				t.Fatal(err)
			}
			turno := a.st.unico(t)
			a.semLLM()
			if a.sis.CtxChamadas != 0 {
				t.Error("resposta fixa não precisa do contexto")
			}
			if turno.Modo != store.ModoFixa || turno.Resultado != ResultadoOK {
				t.Errorf("turno = %+v", turno)
			}
			if !reflect.DeepEqual(a.sis.Enviadas, []string{c.quer}) {
				t.Errorf("enviadas = %v", a.sis.Enviadas)
			}
			if want := []string{"cliente:" + c.mem, "agente:" + c.quer}; !reflect.DeepEqual(a.janela(), want) {
				t.Errorf("memória = %v", a.janela())
			}
		})
	}
}

func TestMidiaFixaUsaRespostaDaConfigGuardada(t *testing.T) {
	a := novoAmb(t)
	custom := "Só texto, por favor!"
	cfg, _ := json.Marshal(sistema.Atendente{Respostas: sistema.Respostas{MidiaNaoSuportada: &custom}})
	a.st.GuardarConfig(context.Background(), conversaDeTeste, cfg)
	tu := a.turno()
	tu.Mensagens = []mensagem.Mensagem{{ID: "m1", Anexos: []mensagem.Anexo{{Tipo: "imagem"}}}}
	a.motor.Run(context.Background(), tu)
	if !reflect.DeepEqual(a.sis.Enviadas, []string{custom}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
}

func TestTextoMaisAudioVaiAoLLMComMarcador(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Pode escrever?")}
	tu := a.turno("oi")
	tu.Mensagens = append(tu.Mensagens, mensagem.Mensagem{ID: "m2", Anexos: []mensagem.Anexo{{Tipo: "audio", DuracaoS: 5}}})
	a.motor.Run(context.Background(), tu)
	req := a.llm.Reqs[0]
	if got := req.Contents[len(req.Contents)-1].Parts[0].Text; got != "oi\n[áudio 5s]" {
		t.Errorf("turno do cliente = %q", got)
	}
}

func TestRajadaViraUmTurnoDoClienteSeparadoPorQuebraDeLinha(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Certo.")}
	tu := a.run("oi", "queria marcar", "pra sexta")
	req := a.llm.Reqs[0]
	if got := req.Contents[len(req.Contents)-1].Parts[0].Text; got != "oi\nqueria marcar\npra sexta" {
		t.Errorf("turno = %q", got)
	}
	if !reflect.DeepEqual(tu.MensagemIDs, []string{"in-1", "in-2", "in-3"}) {
		t.Errorf("ids = %v", tu.MensagemIDs)
	}
	if want := []string{"cliente:oi\nqueria marcar\npra sexta", "agente:Certo."}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v", a.janela())
	}
}

func TestMemoriaEntraComoHistoricoComPapeis(t *testing.T) {
	a := novoAmb(t)
	ctx := context.Background()
	ptr := func(s string) *string { return &s }
	for _, m := range []store.MensagemMemoria{
		{Papel: store.PapelCliente, Conteudo: ptr("oi")},
		{Papel: store.PapelAgente, Conteudo: ptr("olá!")},
		{Papel: store.PapelHumano, Conteudo: ptr("aqui é a Bia")},
		{Papel: store.PapelCliente, Conteudo: nil}, // expurgada pela retenção
	} {
		if err := a.st.AnexarMensagem(ctx, conversaDeTeste, m); err != nil {
			t.Fatal(err)
		}
	}
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("ok")}
	a.run("e então?")
	c := a.llm.Reqs[0].Contents
	type rt struct{ r, t string }
	var got []rt
	for _, x := range c {
		got = append(got, rt{x.Role, x.Parts[0].Text})
	}
	want := []rt{{"user", "oi"}, {"model", "olá!"}, {"user", "[equipe] aqui é a Bia"}, {"user", "e então?"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("contents = %v", got)
	}
}

func TestChamarHumano(t *testing.T) {
	cenarios := []struct {
		nome        string
		args        map[string]any
		escalarErr  error
		wantResp    map[string]any
		wantEscalou bool
		wantEscalas int
	}{
		{"ok", map[string]any{"motivo": "reclamacao", "resumo": "cliente irritado"}, nil,
			map[string]any{"equipeNotificada": true}, true, 1},
		{"rate limited nao derruba", map[string]any{"motivo": "pedido_do_cliente", "resumo": "x"},
			&conector.ErrAbortar{Code: "RATE_LIMITED", Status: 429}, map[string]any{"equipeNotificada": false}, false, 1},
		{"erro de regra do escalar tambem nao derruba", map[string]any{"motivo": "outro", "resumo": "x"},
			&sistema.ErrSistema{Code: "VALIDATION_ERROR", Status: 422}, map[string]any{"equipeNotificada": false}, false, 1},
		{"motivo invalido", map[string]any{"motivo": "falha_tecnica", "resumo": "x"}, nil,
			map[string]any{"erro": "ARGUMENTO_INVALIDO", "mensagem": "motivo inválido; use um dos valores permitidos"}, false, 0},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmb(t)
			a.sis.EscalarErrs = []error{c.escalarErr}
			a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("chamar_humano", c.args), llm.TextoResp("A equipe vai ver.")}
			tu := a.run("quero falar com alguém")

			fr := a.llm.Reqs[1].Contents[len(a.llm.Reqs[1].Contents)-1].Parts[0].FunctionResponse
			if fr.Name != "chamar_humano" || !reflect.DeepEqual(fr.Response, c.wantResp) {
				t.Errorf("functionResponse = %+v", fr)
			}
			if tu.Resultado != ResultadoOK || tu.Modo != store.ModoLLM || tu.Escalou != c.wantEscalou {
				t.Errorf("turno = %+v", tu)
			}
			if len(a.sis.Escaladas) != c.wantEscalas {
				t.Errorf("escaladas = %+v", a.sis.Escaladas)
			}
			if !reflect.DeepEqual(a.sis.Enviadas, []string{"A equipe vai ver."}) {
				t.Errorf("enviadas = %v (o turno não pode virar F2)", a.sis.Enviadas)
			}
		})
	}
}

func TestChamarHumanoTruncaResumoEm1000(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("chamar_humano", map[string]any{"motivo": "outro", "resumo": strings.Repeat("é", 1500)}),
		llm.TextoResp("ok"),
	}
	a.run("oi")
	if n := len([]rune(a.sis.Escaladas[0].Resumo)); n != 1000 {
		t.Errorf("resumo com %d runes", n)
	}
}

func TestTextoLongoEhDivididoEmBlocosEOsEcosSaoRegistrados(t *testing.T) {
	a := novoAmb(t)
	par := strings.Repeat("a", 1500)
	longo := par + "\n\n" + par + "\n\n" + par + "\n\n" + par + "\n\n" + par
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp(longo)}
	tu := a.run("conta tudo")

	if len(a.sis.Enviadas) != 3 {
		t.Fatalf("blocos = %d, quer 3", len(a.sis.Enviadas))
	}
	for i, b := range a.sis.Enviadas {
		if n := len([]rune(b)); n > LimiteMensagem || n == 0 {
			t.Errorf("bloco %d com %d runes", i, n)
		}
	}
	if got := strings.Join(a.sis.Enviadas, "\n\n"); got != longo {
		t.Error("os blocos em ordem não reconstituem o texto")
	}
	for _, id := range []string{"out-1", "out-2", "out-3"} {
		if eco, _ := a.st.EhEco(context.Background(), "eliza", id); !eco {
			t.Errorf("eco %s não registrado", id)
		}
	}
	if tu.Resultado != ResultadoOK {
		t.Errorf("resultado = %s", tu.Resultado)
	}
	if want := []string{"cliente:conta tudo", "agente:" + longo}; !reflect.DeepEqual(a.janela(), want) {
		t.Error("a memória deve guardar a resposta completa")
	}
}

func TestEcoSemIdLogaEPulaORegistro(t *testing.T) {
	a := novoAmb(t)
	a.sis.SemID = true
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Oi!")}
	a.run("oi")
	contem(t, a.logs.String(), `"msg":"eco_sem_id"`)
	if eco, _ := a.st.EhEco(context.Background(), "eliza", ""); eco {
		t.Error("eco vazio registrado")
	}
}

func TestErroDeEnvioNaoReenviaEMarcaErroEnvio(t *testing.T) {
	a := novoAmb(t)
	a.sis.EnvioErr = &conector.ErrAbortar{Code: "WHATSAPP_UNAVAILABLE", Status: 503}
	a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("criarAgendamento", map[string]any{"inicio": "x"}), llm.TextoResp("Marquei.")}
	tu := a.run("marca")
	if tu.Resultado != ResultadoErroEnvio || tu.Modo != store.ModoLLM || tu.Escalou {
		t.Errorf("turno = %+v", tu)
	}
	if len(a.sis.Escaladas) != 0 || len(a.sis.Enviadas) != 0 {
		t.Error("erro de envio não reenvia nem escala")
	}
	if want := []string{"cliente:marca"}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v (a resposta não saiu)", a.janela())
	}
}

func TestLogNuncaTemTextoTicketNemArgumentos(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{
		llm.CallResp("criarAgendamento", map[string]any{"inicio": "ARGUMENTO-SECRETO", "observacao": "OBS-SECRETA"}),
		llm.TextoResp("RESPOSTA-SECRETA do modelo"),
	}
	tu := a.turno("TEXTO-SECRETO do cliente")
	tu.Ticket = "TICKET-SECRETO"
	if err := a.motor.Run(context.Background(), tu); err != nil {
		t.Fatal(err)
	}
	logs := a.logs.String()
	for _, segredo := range []string{"SECRETO", "SECRETA", conversaDeTeste.Contato} {
		if strings.Contains(logs, segredo) {
			t.Errorf("o log contém %q:\n%s", segredo, logs)
		}
	}
	contem(t, logs, mensagem.HashContato(conversaDeTeste.Contato))
}

func TestLogDebugLiberaArgumentos(t *testing.T) {
	a := novoAmb(t)
	debug := &bufSeguro{}
	a.motor.Log = slogDebug(debug)
	a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("listarServicos", map[string]any{"q": "ARG-DEBUG"}), llm.TextoResp("ok")}
	a.run("oi")
	contem(t, debug.String(), "ARG-DEBUG")
}

func TestConfigDoContextoEhGuardadaEMesclada(t *testing.T) {
	a := novoAmb(t)
	persona, hrs, conf := "Lia", 3, "Fechado, {primeiroNome}!"
	a.sis.Ctx.Atendente = sistema.Atendente{Persona: &persona, PausaHumanaHoras: &hrs, Respostas: sistema.Respostas{Confirmado: &conf}}
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Oi")}
	a.run("oi")
	contem(t, a.llm.Reqs[0].System, "Você é Lia, atendente de Studio Bela")
	c, _ := a.st.Conversa(context.Background(), conversaDeTeste)
	var guardada sistema.Atendente
	if err := json.Unmarshal(c.ConfigJSON, &guardada); err != nil || guardada.PausaHumanaHoras == nil || *guardada.PausaHumanaHoras != 3 {
		t.Errorf("config guardada = %s (%v)", c.ConfigJSON, err)
	}
}

func TestStatusHTTPDaEscritaEhGetOuNao(t *testing.T) {
	// escrita = método ≠ GET com sucesso; GET não conta para o fallback "Feito!"
	a := novoAmb(t)
	a.ex.Fn = func(f openapiFerramenta, _ map[string]any) (conector.Resultado, error) {
		if f.Metodo == http.MethodPost {
			return conector.Resultado{Status: 422, ErroRegra: &conector.ErroRegra{Erro: "X"}}, nil
		}
		return conector.Resultado{Dados: json.RawMessage(`{}`), Status: 200}, nil
	}
	vazia := llm.GenerateResponse{FinishReason: "STOP"}
	a.llm.Respostas = []llm.GenerateResponse{llm.CallResp("criarAgendamento", map[string]any{"inicio": "x"}), vazia, vazia}
	tu := a.run("marca")
	if tu.Modo != store.ModoErro {
		t.Errorf("POST recusado não é escrita: turno = %+v", tu)
	}
}
