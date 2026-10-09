package agent

import (
	"errors"
	"strings"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
	"sola-agens/internal/llm"
	"sola-agens/internal/mensagem"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
	"sola-agens/internal/transcricao"
)

// maxAudioBytes é o teto do áudio, informado e baixado (specs/03 F5 passos 3–4).
const maxAudioBytes = 16 << 20

// desfecho é o fim antecipado de um turno com áudio: uma resposta fixa, ou o
// ticket que deixou de valer durante o download.
type desfecho struct {
	texto, resultado string
	codTicket        string // preenchido = ticket_invalido, sem resposta
}

func fixa(texto string) *desfecho { return &desfecho{texto: texto, resultado: ResultadoOK} }

// temAudioProcessar diz se algum anexo é áudio com política "processar".
func (e *turnoExec) temAudioProcessar() bool {
	for _, msg := range e.t.Mensagens {
		for _, a := range msg.Anexos {
			if a.Tipo == "audio" && e.politica("audio") == agentes.MidiaProcessar {
				return true
			}
		}
	}
	return false
}

// encerrarAudio fecha o turno no fim antecipado da F5.
func (e *turnoExec) encerrarAudio(d *desfecho) error {
	if d.codTicket != "" {
		return e.ticketInvalido(d.codTicket)
	}
	return e.concluirCom(store.ModoFixa, d.texto, d.resultado)
}

// processarAudios é a F5 (specs/03): cada áudio da rajada, na ordem, é baixado
// com o ticket da própria mensagem, transcrito e vira o texto dela. Devolve o
// fim antecipado (desfecho) ou uma falha técnica (F2); com os dois nil, o turno
// segue com e.textoLLM/e.textoMem já preenchidos.
// DECISÃO: o primeiro áudio que não pode ser processado encerra o turno inteiro
// com a resposta fixa dele, mesmo que haja texto ou outros áudios na rajada
// (o LLM não é chamado); o que já tinha sido transcrito é descartado.
func (e *turnoExec) processarAudios() (*desfecho, *falha) {
	var mem, llmTxt []string
	unico := "" // transcrição, se o turno é um único áudio sem texto
	for _, msg := range e.t.Mensagens {
		var pm, pl []string
		if msg.Texto != "" {
			pm, pl = append(pm, msg.Texto), append(pl, msg.Texto)
		}
		for _, a := range msg.Anexos {
			if a.Tipo != "audio" || e.politica("audio") != agentes.MidiaProcessar {
				pm, pl = append(pm, marcador(a)), append(pl, marcador(a))
				continue
			}
			texto, d, f := e.transcrever(msg, a)
			if d != nil || f != nil {
				return d, f
			}
			pm, pl = append(pm, marcador(a)+" "+texto), append(pl, texto)
			if len(e.t.Mensagens) == 1 && len(msg.Anexos) == 1 && msg.Texto == "" {
				unico = texto
			}
		}
		mem, llmTxt = append(mem, strings.Join(pm, "\n")), append(llmTxt, strings.Join(pl, "\n"))
	}
	e.textoMem, e.textoLLM, e.fpTexto = strings.Join(mem, "\n"), strings.Join(llmTxt, "\n"), unico
	return nil, nil
}

// transcrever cobre os passos 2–5 da F5 para um anexo de áudio.
func (e *turnoExec) transcrever(msg mensagem.Mensagem, a mensagem.Anexo) (string, *desfecho, *falha) {
	audioLongo := fixa(Aplicar(e.dic.Respostas.AudioLongo, ""))
	if max := e.ag.Registro.Limites.AudioMaxS; max > 0 && a.DuracaoS > max {
		return "", audioLongo, nil
	}
	if a.Tamanho > maxAudioBytes {
		return "", audioLongo, nil
	}

	ticket := msg.Ticket
	if ticket == "" {
		ticket = e.t.Ticket // DECISÃO: sem ticket próprio (Turno montado fora do atendimento), vale o mais recente
	}
	ref := a.Ref
	if ref == "" {
		ref = msg.ID
	}
	midia, err := e.ag.Sistema.BaixarMidia(e.ctx, conector.Identidade{Ticket: ticket}, ref)
	if err != nil {
		var ab *conector.ErrAbortar
		switch {
		case errors.Is(err, sistema.ErrNotFound):
			e.log.Info("audio_nao_chegou")
			return "", fixa(Aplicar(e.dic.Respostas.AudioNaoChegou, "")), nil
		case errors.Is(err, sistema.ErrMediaTooLarge):
			return "", audioLongo, nil
		case errors.As(err, &ab) && codigosTicket[ab.Code]:
			return "", &desfecho{codTicket: ab.Code}, nil
		}
		return "", nil, &falha{etapa: "mídia", resultado: ResultadoErroSistema, causa: err}
	}
	if len(midia.Bytes) > maxAudioBytes {
		return "", audioLongo, nil
	}

	mime := midia.Mime
	if mime == "" {
		mime = a.Mime
	}
	inicio := time.Now()
	texto, uso, err := e.m.Transcritor.Transcrever(e.ctx, midia.Bytes, limparMime(mime), transcricao.Idioma)
	midia.Bytes = nil // descartado: nada de áudio fica no turno nem em disco
	e.somarUso(uso)
	texto = strings.TrimSpace(texto)
	if err != nil || texto == "" {
		// Nunca o texto nem o áudio no log; err vem do provedor (status/rede).
		motivo := "vazia"
		if err != nil {
			motivo = err.Error()
		}
		e.log.Error("transcricao_falhou", "dur_s", a.DuracaoS, "erro", motivo)
		// Sem escalar: o cliente pode escrever (F5 passo 5).
		// Revisão Opus: falhaTecnica diz "já avisei a equipe", e aqui não se escala.
		return "", &desfecho{texto: Aplicar(e.dic.Respostas.AudioFalhou, ""), resultado: ResultadoErroTranscricao}, nil
	}
	e.log.Info("audio_transcrito", "dur_s", a.DuracaoS, "ms", time.Since(inicio).Milliseconds(),
		"tokens_entrada", uso.TokensEntrada, "tokens_saida", uso.TokensSaida)
	return texto, nil, nil
}

// somarUso soma os tokens da transcrição ao turno. O custo é calculado à
// parte, com o modelo que transcreveu (u.Modelo), não com o do agente.
// Estimativa: o Gemini cobra áudio de entrada acima do preço de texto.
func (e *turnoExec) somarUso(u transcricao.Uso) {
	e.tin += u.TokensEntrada
	e.tout += u.TokensSaida
	if u.Modelo != "" {
		e.custoTranscricao += llm.Custo(u.Modelo, u.TokensEntrada, u.TokensSaida)
		e.tinTransc += u.TokensEntrada
		e.toutTransc += u.TokensSaida
	}
}

// limparMime tira parâmetros do mime ("audio/ogg; codecs=opus" → "audio/ogg"):
// o Gemini devolve 400 com parâmetros extras (specs/04 §2.2).
func limparMime(m string) string {
	if i := strings.Index(m, ";"); i >= 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}
