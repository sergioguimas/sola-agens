// Package transcricao implementa o Transcritor (specs/04 §4, copiado do
// Smaug): converte áudio em texto, sem ferramentas, memória ou system prompt.
// Duas implementações, escolhidas por config.Config.Transcricao
// (AGENS_TRANSCRICAO): gemini.go reaproveita o llm.Provider existente;
// groq.go fala REST cru com a API de transcrição do Groq (mime/multipart +
// net/http da stdlib, sem dependência nova).
package transcricao

import (
	"context"
	"fmt"
	"net/http"

	"sola-agens/internal/llm"
)

// Uso são os tokens que a transcrição gastou (só o Gemini informa; o Groq
// devolve zero). O agente soma ao custo do turno.
// DECISÃO: o Smaug devolvia só o texto; aqui a interface devolve também o Uso,
// porque specs/01 §8 manda somar a transcrição pelo Gemini ao turno.
type Uso struct {
	TokensEntrada, TokensSaida int
	Modelo                     string // vazio = provedor sem tokens (Groq)
}

// Transcritor converte áudio em texto (specs/04 §2.6).
type Transcritor interface {
	Transcrever(ctx context.Context, audio []byte, mimetype, idioma string) (string, Uso, error)
}

// groqDefaultURL e o endpoint padrao da API de transcricao do Groq
// (specs/04 §2.6).
const modeloGroqPadrao = "whisper-large-v3-turbo"

// Idioma é o idioma pedido ao provedor (o agens atende em português).
const Idioma = "pt"

const groqDefaultURL = "https://api.groq.com/openai/v1/audio/transcriptions"

// Config sao os parametros para montar o Transcritor (CONTRATO §6,
// specs/04 §2.6).
type Config struct {
	// Provedor e "gemini" ou "groq" — ja resolvido (config.LoadConfigFrom
	// resolve AGENS_TRANSCRICAO=auto antes de chegar aqui).
	Provedor     string
	GroqKey      string
	GroqModel    string
	GroqURL      string // vazio usa groqDefaultURL; sobrescrevivel no teste (httptest.Server)
	ModeloGemini string // AGENS_MODEL (specs/02 §6)
}

// New monta o Transcritor de acordo com cfg.Provedor.
//
// DECISÃO: o provedor "gemini" já tem log por tentativa (evt=llm, via
// llm.Provider); o "groq" não loga por chamada — o agente loga o turno
// (duração e tokens, nunca o texto).
func New(cfg Config, provider llm.Provider, httpClient *http.Client) (Transcritor, error) {
	switch cfg.Provedor {
	case "gemini":
		return &geminiTranscritor{provider: provider, modelo: cfg.ModeloGemini}, nil
	case "groq":
		// DECISÃO: o agens não tem env para o modelo do Groq (a config não o lê);
		// padrão é o whisper turbo, o mesmo do Smaug.
		modelo := cfg.GroqModel
		if modelo == "" {
			modelo = modeloGroqPadrao
		}
		url := cfg.GroqURL
		if url == "" {
			url = groqDefaultURL
		}
		if httpClient == nil {
			httpClient = &http.Client{}
		}
		return &groqTranscritor{apiKey: cfg.GroqKey, modelo: modelo, url: url, httpClient: httpClient}, nil
	default:
		return nil, fmt.Errorf("transcricao: provedor desconhecido %q (use gemini ou groq)", cfg.Provedor)
	}
}
