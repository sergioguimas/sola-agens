package gemini

import (
	"encoding/base64"

	"sola-agens/internal/llm"
)

// Tipos de wire do request da API Gemini v1beta generateContent
// (specs/04 §2.2). Nomes de campo conferidos na doc oficial em 2026;
// note que system_instruction e snake_case enquanto o resto e camelCase —
// e assim mesmo na API.
type generateRequest struct {
	SystemInstruction *content          `json:"system_instruction,omitempty"`
	Contents          []content         `json:"contents"`
	Tools             []toolsWrapper    `json:"tools,omitempty"`
	ToolConfig        *toolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string                `json:"text,omitempty"`
	InlineData       *inlineData           `json:"inlineData,omitempty"`
	FunctionCall     *functionCallWire     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponseWire `json:"functionResponse,omitempty"`
	ThoughtSignature string                `json:"thoughtSignature,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type functionCallWire struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type functionResponseWire struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"` // sempre presente: a API exige, mesmo vazio
}

type toolsWrapper struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type toolConfig struct {
	FunctionCallingConfig functionCallingConfig `json:"functionCallingConfig"`
}

type functionCallingConfig struct {
	Mode string `json:"mode"`
}

type generationConfig struct {
	Temperature     float64         `json:"temperature"`
	MaxOutputTokens int             `json:"maxOutputTokens"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

// buildRequest converte o llm.GenerateRequest generico no formato de wire
// da API Gemini (specs/04 §2.2).
func buildRequest(req llm.GenerateRequest) generateRequest {
	gr := generateRequest{
		Contents: make([]content, 0, len(req.Contents)),
		GenerationConfig: &generationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxOutputTok,
		},
	}

	if req.System != "" {
		gr.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}

	for _, t := range req.Contents {
		gr.Contents = append(gr.Contents, toContent(t))
	}

	if len(req.Tools) > 0 {
		decls := make([]functionDeclaration, 0, len(req.Tools))
		for _, td := range req.Tools {
			decls = append(decls, functionDeclaration{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			})
		}
		gr.Tools = []toolsWrapper{{FunctionDeclarations: decls}}
		gr.ToolConfig = &toolConfig{FunctionCallingConfig: functionCallingConfig{Mode: "AUTO"}}
	}

	if req.ThinkingBudget != nil {
		gr.GenerationConfig.ThinkingConfig = &thinkingConfig{ThinkingBudget: *req.ThinkingBudget}
	}

	return gr
}

func toContent(t llm.Turn) content {
	c := content{Role: t.Role, Parts: make([]part, 0, len(t.Parts))}
	for _, p := range t.Parts {
		c.Parts = append(c.Parts, toPart(p))
	}
	return c
}

// toPart converte uma llm.Part (exatamente um campo preenchido) na parte
// de wire correspondente.
func toPart(p llm.Part) part {
	switch {
	case p.FunctionCall != nil:
		return part{FunctionCall: &functionCallWire{Name: p.FunctionCall.Name, Args: p.FunctionCall.Args}, ThoughtSignature: p.ThoughtSignature}
	case p.FunctionResponse != nil:
		return part{FunctionResponse: &functionResponseWire{Name: p.FunctionResponse.Name, Response: p.FunctionResponse.Response}}
	case p.InlineMime != "":
		return part{InlineData: &inlineData{MimeType: p.InlineMime, Data: base64.StdEncoding.EncodeToString(p.InlineData)}}
	default:
		return part{Text: p.Text}
	}
}
