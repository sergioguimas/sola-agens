package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
	"sola-agens/internal/transcricao"
)

// novoAudio é o ambiente com Transcritor falso montado.
func novoAudio(t *testing.T) (*amb, *transcricao.Fake) {
	t.Helper()
	a := novoAmb(t)
	tr := &transcricao.Fake{}
	a.motor.Transcritor = tr
	return a, tr
}

func msgAudio(id, ticket string, dur int, tam int64) mensagem.Mensagem {
	return mensagem.Mensagem{ID: id, Ticket: ticket, RecebidaEm: time.Date(2026, 10, 9, 14, 30, 0, 0, time.UTC),
		Anexos: []mensagem.Anexo{{Tipo: "audio", Mime: "audio/ogg; codecs=opus", DuracaoS: dur, Tamanho: tam, Ref: id}}}
}

func msgTexto(id, ticket, texto string) mensagem.Mensagem {
	return mensagem.Mensagem{ID: id, Ticket: ticket, Texto: texto, RecebidaEm: time.Date(2026, 10, 9, 14, 30, 0, 0, time.UTC)}
}

func (a *amb) rodar(ms ...mensagem.Mensagem) store.Turno {
	a.t.Helper()
	tu := a.turno()
	tu.Mensagens = ms
	if err := a.motor.Run(context.Background(), tu); err != nil {
		a.t.Fatalf("Run: %v", err)
	}
	return a.st.unico(a.t)
}

func midiaOK(id string) (sistema.Midia, error) {
	return sistema.Midia{Mime: "audio/ogg; codecs=opus", Bytes: []byte("bytes-de-" + id)}, nil
}

func (a *amb) semDownload(tr *transcricao.Fake) {
	a.t.Helper()
	if len(a.sis.Baixas) != 0 || len(tr.Chamadas) != 0 {
		a.t.Errorf("baixou %d e transcreveu %d, quer 0 e 0", len(a.sis.Baixas), len(tr.Chamadas))
	}
}

func TestAudioCurtoViraTexto(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"quero marcar um corte"}
	tr.Usos = []transcricao.Uso{{TokensEntrada: 100, TokensSaida: 5}}
	a.llm.Respostas = []llm.GenerateResponse{respComTokens(llm.TextoResp("Claro! Qual dia?"), 50, 10)}

	turno := a.rodar(msgAudio("au1", "tk-au1", 10, 20000))

	if want := []baixa{{"tk-au1", "au1"}}; !reflect.DeepEqual(a.sis.Baixas, want) {
		t.Errorf("baixas = %+v, quer %+v", a.sis.Baixas, want)
	}
	if len(tr.Chamadas) != 1 || tr.Chamadas[0].Mimetype != "audio/ogg" || tr.Chamadas[0].Idioma != "pt" ||
		string(tr.Chamadas[0].Audio) != "bytes-de-au1" {
		t.Errorf("transcrição = %+v", tr.Chamadas)
	}
	// o LLM vê o texto como se fosse digitado (sem o marcador)
	if got := a.llm.Reqs[0].Contents[len(a.llm.Reqs[0].Contents)-1].Parts[0].Text; got != "quero marcar um corte" {
		t.Errorf("turno do cliente = %q", got)
	}
	if want := []string{"cliente:[áudio 10s] quero marcar um corte", "agente:Claro! Qual dia?"}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v", a.janela())
	}
	if turno.Modo != store.ModoLLM || turno.Resultado != ResultadoOK {
		t.Errorf("turno = %+v", turno)
	}
	if turno.TokensEntrada != 150 || turno.TokensSaida != 15 {
		t.Errorf("tokens = %d/%d, quer 150/15 (transcrição somada)", turno.TokensEntrada, turno.TokensSaida)
	}
}

func TestRajadaTextoEAudioPreservaOrdemECadaTicket(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"primeiro áudio", "segundo áudio"}
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Certo.")}

	a.rodar(msgTexto("t1", "tk-t1", "oi"), msgAudio("au1", "tk-au1", 5, 0), msgTexto("t2", "tk-t2", "e mais"), msgAudio("au2", "tk-au2", 6, 0))

	want := []baixa{{"tk-au1", "au1"}, {"tk-au2", "au2"}}
	if !reflect.DeepEqual(a.sis.Baixas, want) {
		t.Errorf("baixas = %+v, quer %+v", a.sis.Baixas, want)
	}
	if got := a.llm.Reqs[0].Contents[len(a.llm.Reqs[0].Contents)-1].Parts[0].Text; got != "oi\nprimeiro áudio\ne mais\nsegundo áudio" {
		t.Errorf("turno do cliente = %q", got)
	}
	wantMem := "cliente:oi\n[áudio 5s] primeiro áudio\ne mais\n[áudio 6s] segundo áudio"
	if j := a.janela(); len(j) != 2 || j[0] != wantMem {
		t.Errorf("memória = %q", j)
	}
}

func TestAudioSemTicketProprioUsaOMaisRecente(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"oi"}
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Olá!")}
	a.rodar(msgAudio("au1", "", 5, 0))
	if len(a.sis.Baixas) != 1 || a.sis.Baixas[0].Ticket != "tk-1" {
		t.Errorf("baixas = %+v", a.sis.Baixas)
	}
}

func TestAudioQueNaoPodeSerProcessado(t *testing.T) {
	grande := make([]byte, maxAudioBytes+1)
	cenarios := []struct {
		nome        string
		ms          []mensagem.Mensagem
		midia       func(string) (sistema.Midia, error)
		quer        string // resposta enviada
		baixou      int
		transcreveu int
	}{
		{"duração acima do limite", []mensagem.Mensagem{msgAudio("au1", "t", 121, 0)}, midiaOK, Padrao().Respostas.AudioLongo, 0, 0},
		{"áudio longo com texto na rajada: só o audioLongo", []mensagem.Mensagem{msgTexto("t1", "t", "oi"), msgAudio("au1", "t", 500, 0)},
			midiaOK, Padrao().Respostas.AudioLongo, 0, 0},
		{"tamanho informado acima de 16 MB", []mensagem.Mensagem{msgAudio("au1", "t", 10, maxAudioBytes+1)}, midiaOK,
			Padrao().Respostas.AudioLongo, 0, 0},
		{"NOT_FOUND", []mensagem.Mensagem{msgAudio("au1", "t", 10, 0)},
			func(string) (sistema.Midia, error) {
				return sistema.Midia{}, &sistema.ErrSistema{Code: "NOT_FOUND", Status: 404}
			},
			Padrao().Respostas.AudioNaoChegou, 1, 0},
		{"MEDIA_TOO_LARGE", []mensagem.Mensagem{msgAudio("au1", "t", 10, 0)},
			func(string) (sistema.Midia, error) {
				return sistema.Midia{}, &sistema.ErrSistema{Code: "MEDIA_TOO_LARGE", Status: 422}
			},
			Padrao().Respostas.AudioLongo, 1, 0},
		{"bytes acima de 16 MB depois do download", []mensagem.Mensagem{msgAudio("au1", "t", 10, 0)},
			func(string) (sistema.Midia, error) { return sistema.Midia{Mime: "audio/ogg", Bytes: grande}, nil },
			Padrao().Respostas.AudioLongo, 1, 0},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			a, tr := novoAudio(t)
			a.sis.MidiaFn = c.midia
			turno := a.rodar(c.ms...)
			a.semLLM()
			if turno.Modo != store.ModoFixa || turno.Resultado != ResultadoOK || turno.Escalou {
				t.Errorf("turno = %+v", turno)
			}
			if !reflect.DeepEqual(a.sis.Enviadas, []string{c.quer}) {
				t.Errorf("enviadas = %v, quer %q", a.sis.Enviadas, c.quer)
			}
			if len(a.sis.Baixas) != c.baixou || len(tr.Chamadas) != c.transcreveu {
				t.Errorf("baixou %d, transcreveu %d; quer %d e %d", len(a.sis.Baixas), len(tr.Chamadas), c.baixou, c.transcreveu)
			}
			if len(a.sis.Escaladas) != 0 {
				t.Errorf("escalou: %+v", a.sis.Escaladas)
			}
			if a.sis.CtxChamadas != 0 {
				t.Error("fim antecipado não precisa do contexto")
			}
		})
	}
}

func TestAudioErroTecnicoDoDownloadVaiParaF2(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = func(string) (sistema.Midia, error) {
		return sistema.Midia{}, &conector.ErrAbortar{Code: "WHATSAPP_UNAVAILABLE", Status: 502}
	}
	turno := a.rodar(msgAudio("au1", "t", 10, 0))
	a.semLLM()
	if turno.Modo != store.ModoErro || turno.Resultado != ResultadoErroSistema || !turno.Escalou {
		t.Errorf("turno = %+v", turno)
	}
	if want := []string{Padrao().Respostas.FalhaTecnica}; !reflect.DeepEqual(a.sis.Enviadas, want) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
	if len(a.sis.Escaladas) != 1 || a.sis.Escaladas[0].Motivo != "falha_tecnica" ||
		!strings.Contains(a.sis.Escaladas[0].Resumo, "mídia") {
		t.Errorf("escaladas = %+v", a.sis.Escaladas)
	}
	if len(tr.Chamadas) != 0 {
		t.Error("não deveria transcrever")
	}
}

func TestAudioComTicketInvalidoNoDownload(t *testing.T) {
	a, _ := novoAudio(t)
	a.sis.MidiaFn = func(string) (sistema.Midia, error) {
		return sistema.Midia{}, &conector.ErrAbortar{Code: "TICKET_EXPIRED", Status: 401}
	}
	turno := a.rodar(msgAudio("au1", "t", 10, 0))
	if turno.Resultado != ResultadoTicketInvalid || turno.Modo != store.ModoErro || len(a.sis.Enviadas) != 0 {
		t.Errorf("turno = %+v, enviadas = %v", turno, a.sis.Enviadas)
	}
}

func TestFalhaDeTranscricaoNaoEscala(t *testing.T) {
	cenarios := []struct {
		nome   string
		textos []string
		erros  []error
	}{
		{"erro do provedor", nil, []error{errors.New("groq indisponível")}},
		{"transcrição vazia", []string{"   "}, nil},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			a, tr := novoAudio(t)
			tr.Textos, tr.Erros = c.textos, c.erros
			a.sis.MidiaFn = midiaOK
			turno := a.rodar(msgAudio("au1", "t", 10, 0))
			a.semLLM()
			if turno.Modo != store.ModoFixa || turno.Resultado != ResultadoErroTranscricao || turno.Escalou {
				t.Errorf("turno = %+v", turno)
			}
			if want := []string{Padrao().Respostas.AudioFalhou}; !reflect.DeepEqual(a.sis.Enviadas, want) {
				t.Errorf("enviadas = %v", a.sis.Enviadas)
			}
			if len(a.sis.Escaladas) != 0 {
				t.Errorf("não deveria escalar: %+v", a.sis.Escaladas)
			}
			if want := []string{"cliente:[áudio 10s]", "agente:" + Padrao().Respostas.AudioFalhou}; !reflect.DeepEqual(a.janela(), want) {
				t.Errorf("memória = %v", a.janela())
			}
		})
	}
}

func TestAudioSimComUmAguardandoConfirmaPeloFastpath(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"Sim."}
	a.sis.Ctx.AguardandoConfirmacao = []string{"ag1"}

	turno := a.rodar(msgAudio("au1", "tk-au1", 3, 0))

	a.semLLM()
	if turno.Modo != store.ModoFastpath {
		t.Fatalf("modo = %s", turno.Modo)
	}
	if got := a.ex.nomes(); !reflect.DeepEqual(got, []string{"confirmarAgendamento"}) {
		t.Errorf("ferramentas = %v", got)
	}
	if want := []string{"cliente:[áudio 3s] Sim.", "agente:Prontinho, Ana! Seu horário está confirmado."}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v", a.janela())
	}
}

func TestAudioNaoDisparaFastpathNaRajada(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"sim"}
	a.sis.Ctx.AguardandoConfirmacao = []string{"ag1"}
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("ok")}
	turno := a.rodar(msgTexto("t1", "t", "oi"), msgAudio("au1", "t", 3, 0))
	if turno.Modo != store.ModoLLM {
		t.Errorf("modo = %s", turno.Modo)
	}
}

func TestPoliticaRespostaFixaNaoBaixa(t *testing.T) {
	a, tr := novoAudio(t)
	a.reg.Midia["audio"] = "resposta_fixa"
	a.montar()
	turno := a.rodar(msgAudio("au1", "t", 10, 0))
	a.semLLM()
	a.semDownload(tr)
	if turno.Modo != store.ModoFixa || !reflect.DeepEqual(a.sis.Enviadas, []string{Padrao().Respostas.MidiaNaoSuportada}) {
		t.Errorf("turno = %+v, enviadas = %v", turno, a.sis.Enviadas)
	}
}

func TestLogNaoTemATranscricaoNemTicketNemAudio(t *testing.T) {
	a, tr := novoAudio(t)
	a.motor.Log = slogDebug(a.logs)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"minha transcricao secreta zeta"}
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Resposta ao cliente")}
	a.rodar(msgAudio("au1", "tk-au1-secreto", 10, 0))

	// e também no caminho de falha
	b, trb := novoAudio(t)
	b.motor.Log = slogDebug(a.logs)
	b.sis.MidiaFn = midiaOK
	trb.Erros = []error{errors.New("provedor fora")}
	b.rodar(msgAudio("au2", "tk-au2-secreto", 10, 0))

	logs := a.logs.String()
	if !strings.Contains(logs, "audio_transcrito") || !strings.Contains(logs, "transcricao_falhou") {
		t.Fatalf("faltam os eventos de áudio no log: %s", logs)
	}
	for _, proibido := range []string{"transcricao secreta", "zeta", "tk-au1-secreto", "tk-au2-secreto", "bytes-de-au1", "Ynl0ZXM", "5511999990000"} {
		if strings.Contains(logs, proibido) {
			t.Errorf("o log contém %q", proibido)
		}
	}
}

func TestLimparMime(t *testing.T) {
	for entrada, quer := range map[string]string{
		"audio/ogg; codecs=opus": "audio/ogg", "audio/ogg;codecs=opus": "audio/ogg", " audio/mpeg ": "audio/mpeg", "": "",
	} {
		if got := limparMime(entrada); got != quer {
			t.Errorf("limparMime(%q) = %q, quer %q", entrada, got, quer)
		}
	}
}

// O custo da transcrição usa o modelo que transcreveu, não o do agente.
func TestAudioCustoPorModeloDaTranscricao(t *testing.T) {
	a, tr := novoAudio(t)
	a.sis.MidiaFn = midiaOK
	tr.Textos = []string{"quero marcar"}
	tr.Usos = []transcricao.Uso{{TokensEntrada: 1_000_000, TokensSaida: 0, Modelo: "gemini-2.5-flash-lite"}}
	a.llm.Respostas = []llm.GenerateResponse{respComTokens(llm.TextoResp("Claro!"), 1_000_000, 0)}

	turno := a.rodar(msgAudio("au1", "tk-au1", 10, 20000))

	want := llm.Custo(turno.Modelo, 1_000_000, 0) + llm.Custo("gemini-2.5-flash-lite", 1_000_000, 0)
	if turno.CustoMicroUSD != want || turno.TokensEntrada != 2_000_000 {
		t.Errorf("custo = %d tokens = %d, quer custo %d tokens 2000000 (modelo %q)", turno.CustoMicroUSD, turno.TokensEntrada, want, turno.Modelo)
	}
}
