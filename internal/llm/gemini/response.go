package gemini

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"sola-agens/internal/llm"
)

// generateResponseWire e a resposta de generateContent (specs/04 §2.3).
type generateResponseWire struct {
	Candidates    []candidateWire `json:"candidates"`
	UsageMetadata *usageMetadata  `json:"usageMetadata"`
}

type candidateWire struct {
	Content      *content `json:"content"`
	FinishReason string   `json:"finishReason"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// finishReasons que indicam que a resposta nao e um "STOP" normal
// (specs/04 §2.3).
var finishReasoesErro = map[string]bool{
	"MAX_TOKENS": true,
	"SAFETY":     true,
	"RECITATION": true,
	"OTHER":      true,
}

// parseResponse decodifica o corpo JSON de generateContent. Devolve os
// tokens totais mesmo quando o resultado e um erro (para o log poder
// registrar quantos tokens foram gastos numa resposta ruim).
func parseResponse(raw []byte) (resp *llm.GenerateResponse, tokens int, err error) {
	var gr generateResponseWire
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, 0, fmt.Errorf("gemini: parse resposta: %w", err)
	}

	var in, out int
	if gr.UsageMetadata != nil {
		tokens = gr.UsageMetadata.TotalTokenCount
		in, out = gr.UsageMetadata.PromptTokenCount, gr.UsageMetadata.CandidatesTokenCount
	}

	if len(gr.Candidates) == 0 || gr.Candidates[0].Content == nil {
		return nil, tokens, llm.ErrRespostaVazia
	}

	cand := gr.Candidates[0]
	if finishReasoesErro[cand.FinishReason] {
		return nil, tokens, &llm.ErrFinishReason{Reason: cand.FinishReason}
	}

	parts := make([]llm.Part, 0, len(cand.Content.Parts))
	for _, p := range cand.Content.Parts {
		parts = append(parts, fromPart(p))
	}

	return &llm.GenerateResponse{
		Parts:        parts,
		FinishReason: cand.FinishReason,
		TotalTokens:  tokens,
		InputTokens:  in,
		OutputTokens: out,
	}, tokens, nil
}

func fromPart(p part) llm.Part {
	switch {
	case p.FunctionCall != nil:
		return llm.Part{FunctionCall: &llm.FunctionCall{Name: p.FunctionCall.Name, Args: p.FunctionCall.Args}, ThoughtSignature: p.ThoughtSignature}
	case p.FunctionResponse != nil:
		return llm.Part{FunctionResponse: &llm.FunctionResponse{Name: p.FunctionResponse.Name, Response: p.FunctionResponse.Response}}
	case p.InlineData != nil:
		data, _ := base64.StdEncoding.DecodeString(p.InlineData.Data)
		return llm.Part{InlineMime: p.InlineData.MimeType, InlineData: data}
	default:
		return llm.Part{Text: p.Text}
	}
}

// errorBody e o corpo de erro padrao da API Google em falhas 4xx (specs/04
// §2.4). error.message e conteudo da API, nao do usuario — pode ir ao log.
type errorBody struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func errorMessage(raw []byte) string {
	var eb errorBody
	if err := json.Unmarshal(raw, &eb); err != nil || eb.Error.Message == "" {
		return "erro desconhecido"
	}
	return eb.Error.Message
}
