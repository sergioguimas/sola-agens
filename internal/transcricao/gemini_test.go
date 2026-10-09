package transcricao

import (
	"context"
	"testing"

	"sola-agens/internal/llm"
)

// TestGeminiTranscritor_Request e o golden do GenerateRequest montado pelo
// modo transcricao com AGENS_TRANSCRICAO=gemini (specs/02 §6, specs/04
// §2.6): sem tools, sem system prompt, sem memoria — um unico turno de
// usuario com o audio inline e o prompt fixo.
func TestGeminiTranscritor_Request(t *testing.T) {
	r := llm.TextoResp("texto transcrito")
	r.InputTokens, r.OutputTokens = 120, 7
	fake := &llm.Fake{Respostas: []llm.GenerateResponse{r}}
	tr := &geminiTranscritor{provider: fake, modelo: "gemini-test"}

	audio := []byte{0x01, 0x02, 0x03}
	texto, uso, err := tr.Transcrever(context.Background(), audio, "audio/ogg", "pt")
	if err != nil {
		t.Fatalf("Transcrever: %v", err)
	}
	if texto != "texto transcrito" {
		t.Fatalf("texto = %q", texto)
	}

	if uso != (Uso{TokensEntrada: 120, TokensSaida: 7, Modelo: "gemini-test"}) {
		t.Errorf("uso = %+v", uso)
	}
	if len(fake.Reqs) != 1 {
		t.Fatalf("Reqs = %d, want 1", len(fake.Reqs))
	}
	req := fake.Reqs[0]

	if req.Model != "gemini-test" {
		t.Errorf("Model = %q, want gemini-test", req.Model)
	}
	if req.System != "" {
		t.Errorf("System = %q, want vazio (sem system prompt)", req.System)
	}
	if len(req.Tools) != 0 {
		t.Errorf("Tools = %+v, want vazio (sem ferramentas)", req.Tools)
	}
	if req.MaxOutputTok != 8192 {
		t.Errorf("MaxOutputTok = %d, want 8192", req.MaxOutputTok)
	}
	if req.ThinkingBudget == nil || *req.ThinkingBudget != 0 {
		t.Errorf("ThinkingBudget = %v, want ponteiro para 0", req.ThinkingBudget)
	}
	if len(req.Contents) != 1 {
		t.Fatalf("Contents = %d, want 1 (sem memoria)", len(req.Contents))
	}

	turno := req.Contents[0]
	if turno.Role != "user" || len(turno.Parts) != 2 {
		t.Fatalf("turno = %+v, want user com 2 partes", turno)
	}
	if turno.Parts[0].InlineMime != "audio/ogg" || string(turno.Parts[0].InlineData) != string(audio) {
		t.Errorf("parte 0 (inlineData) = %+v", turno.Parts[0])
	}
	wantPrompt := `Transcreva este áudio em português do Brasil, palavra por palavra, sem comentários, sem cabeçalho, sem marcação de falante. Se houver trechos inaudíveis, marque [inaudível].`
	if turno.Parts[1].Text != wantPrompt {
		t.Errorf("parte 1 (texto) = %q, want %q", turno.Parts[1].Text, wantPrompt)
	}
}

func TestGeminiTranscritor_ErroDoProvider(t *testing.T) {
	fake := &llm.Fake{Err: llm.ErrLLMIndisponivel}
	tr := &geminiTranscritor{provider: fake, modelo: "gemini-test"}

	_, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err == nil {
		t.Fatal("esperava erro")
	}
}

func TestGeminiTranscritor_RespostaVaziaViraStringVazia(t *testing.T) {
	fake := &llm.Fake{Respostas: []llm.GenerateResponse{{Parts: nil, FinishReason: "STOP"}}}
	tr := &geminiTranscritor{provider: fake, modelo: "gemini-test"}

	texto, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err != nil {
		t.Fatalf("Transcrever: %v", err)
	}
	if texto != "" {
		t.Fatalf("texto = %q, want vazio", texto)
	}
}
