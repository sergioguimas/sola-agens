package gemini

import (
	"testing"

	"sola-agens/internal/llm"
)

func TestParseResponse_DuasChamadasParalelas(t *testing.T) {
	raw := []byte(`{
		"candidates": [{
			"content": {"role": "model", "parts": [
				{"functionCall": {"name": "inserir_transacao", "args": {"valor": -12}}},
				{"functionCall": {"name": "listar_categorias", "args": {}}}
			]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 100, "candidatesTokenCount": 40, "totalTokenCount": 140}
	}`)

	resp, tokens, err := parseResponse(raw)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if tokens != 140 || resp.TotalTokens != 140 {
		t.Errorf("tokens = %d, want 140", tokens)
	}
	if resp.InputTokens != 100 || resp.OutputTokens != 40 {
		t.Errorf("in/out = %d/%d, want 100/40", resp.InputTokens, resp.OutputTokens)
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("Parts = %d, want 2", len(resp.Parts))
	}
	if resp.Parts[0].FunctionCall == nil || resp.Parts[0].FunctionCall.Name != "inserir_transacao" {
		t.Errorf("Parts[0] = %+v", resp.Parts[0])
	}
	if resp.Parts[1].FunctionCall == nil || resp.Parts[1].FunctionCall.Name != "listar_categorias" {
		t.Errorf("Parts[1] = %+v", resp.Parts[1])
	}
	if resp.FinishReason != "STOP" {
		t.Errorf("FinishReason = %q", resp.FinishReason)
	}
}

func TestParseResponse_Texto(t *testing.T) {
	raw := []byte(`{"candidates": [{"content": {"role":"model","parts": [{"text": "Lançado."}]}, "finishReason": "STOP"}]}`)
	resp, _, err := parseResponse(raw)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if len(resp.Parts) != 1 || resp.Parts[0].Text != "Lançado." {
		t.Errorf("Parts = %+v", resp.Parts)
	}
}

func TestParseResponse_CandidatesVazio(t *testing.T) {
	raw := []byte(`{"candidates": []}`)
	_, _, err := parseResponse(raw)
	if err != llm.ErrRespostaVazia {
		t.Fatalf("err = %v, want ErrRespostaVazia", err)
	}
}

func TestParseResponse_SemContent(t *testing.T) {
	raw := []byte(`{"candidates": [{"finishReason": "SAFETY"}]}`)
	_, _, err := parseResponse(raw)
	if err != llm.ErrRespostaVazia {
		t.Fatalf("err = %v, want ErrRespostaVazia", err)
	}
}

func TestParseResponse_FinishReasonErro(t *testing.T) {
	tt := []string{"MAX_TOKENS", "SAFETY", "RECITATION", "OTHER"}
	for _, reason := range tt {
		t.Run(reason, func(t *testing.T) {
			raw := []byte(`{"candidates": [{"content": {"parts": [{"text":"x"}]}, "finishReason": "` + reason + `"}]}`)
			_, _, err := parseResponse(raw)
			fe, ok := err.(*llm.ErrFinishReason)
			if !ok {
				t.Fatalf("err = %v (%T), want *ErrFinishReason", err, err)
			}
			if fe.Reason != reason {
				t.Errorf("Reason = %q, want %q", fe.Reason, reason)
			}
		})
	}
}

func TestErrorMessage(t *testing.T) {
	raw := []byte(`{"error": {"code": 400, "message": "API key not valid", "status": "INVALID_ARGUMENT"}}`)
	if msg := errorMessage(raw); msg != "API key not valid" {
		t.Errorf("errorMessage = %q", msg)
	}
	if msg := errorMessage([]byte(`nao e json`)); msg != "erro desconhecido" {
		t.Errorf("errorMessage invalido = %q", msg)
	}
}
