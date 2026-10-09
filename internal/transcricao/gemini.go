package transcricao

import (
	"context"
	"strings"

	"sola-agens/internal/llm"
)

// promptTranscricaoGemini e o texto exato de specs/02 §6 — so usado no
// modo transcricao com AGENS_TRANSCRICAO=gemini (com groq nao ha prompt,
// specs/02 §6 final).
const promptTranscricaoGemini = `Transcreva este áudio em português do Brasil, palavra por palavra, sem comentários, sem cabeçalho, sem marcação de falante. Se houver trechos inaudíveis, marque [inaudível].`

// maxOutputTokTranscricaoGemini (specs/04 §2.6): 10 min de audio ~ 19k
// tokens de ENTRADA; a transcricao de saida cabe folgada em 8192.
const maxOutputTokTranscricaoGemini = 8192

// thinkingBudgetTranscricaoGemini e sempre 0 (specs/04 §2.6) — sem
// "pensamento", so transcricao literal.
var thinkingBudgetTranscricaoGemini = 0

// geminiTranscritor implementa Transcritor sobre um llm.Provider ja
// existente (reaproveita cliente, retry e chave — specs/04 §2.6): um
// unico turno de usuario com o audio inline e o prompt de transcricao,
// sem tools, sem system prompt, sem memoria.
type geminiTranscritor struct {
	provider llm.Provider
	modelo   string
}

var _ Transcritor = (*geminiTranscritor)(nil)

// Transcrever ignora idioma: o prompt de specs/02 §6 ja fixa "português do
// Brasil" — nao ha campo de idioma no request do Gemini (diferente do
// groq, que manda language=pt na API de transcricao).
func (g *geminiTranscritor) Transcrever(ctx context.Context, audio []byte, mimetype, idioma string) (string, Uso, error) {
	req := llm.GenerateRequest{
		Model: g.modelo,
		Contents: []llm.Turn{{
			Role: "user",
			Parts: []llm.Part{
				{InlineMime: mimetype, InlineData: audio},
				{Text: promptTranscricaoGemini},
			},
		}},
		MaxOutputTok:   maxOutputTokTranscricaoGemini,
		ThinkingBudget: &thinkingBudgetTranscricaoGemini,
	}

	resp, err := g.provider.Generate(ctx, req)
	if err != nil {
		return "", Uso{}, err
	}

	var b strings.Builder
	for _, p := range resp.Parts {
		b.WriteString(p.Text)
	}
	return strings.TrimSpace(b.String()), Uso{TokensEntrada: resp.InputTokens, TokensSaida: resp.OutputTokens, Modelo: g.modelo}, nil
}
