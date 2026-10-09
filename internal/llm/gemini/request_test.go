package gemini

import (
	"encoding/json"
	"reflect"
	"testing"

	"sola-agens/internal/llm"
)

// TestBuildRequest_Golden compara o JSON produzido por buildRequest com um
// golden esperado, via map[string]any (nao string crua) para nao depender
// de ordem de chaves (specs/05 §9).
func TestBuildRequest_Golden(t *testing.T) {
	budget := 0
	req := llm.GenerateRequest{
		Model:  "gemini-2.5-flash-lite",
		System: "voce e o smaug",
		Contents: []llm.Turn{
			{Role: "user", Parts: []llm.Part{{Text: "padaria 12"}}},
			{Role: "model", Parts: []llm.Part{
				{FunctionCall: &llm.FunctionCall{Name: "inserir_transacao", Args: map[string]any{"valor": -12.0, "descricao": "Padaria"}}},
			}},
			{Role: "user", Parts: []llm.Part{
				{FunctionResponse: &llm.FunctionResponse{Name: "inserir_transacao", Response: map[string]any{"transacoes": []any{map[string]any{"id": 42.0}}}}},
			}},
			{Role: "user", Parts: []llm.Part{
				{Text: "nota fiscal em anexo"},
				{InlineMime: "image/jpeg", InlineData: []byte("fakejpeg")},
			}},
		},
		Tools: []llm.ToolDecl{
			{Name: "inserir_transacao", Description: "insere um lancamento", Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"valor": map[string]any{"type": "number"}},
			}},
		},
		Temperature:    0.2,
		MaxOutputTok:   1024,
		ThinkingBudget: &budget,
	}

	gotRaw, err := json.Marshal(buildRequest(req))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	wantJSON := `{
		"system_instruction": {"parts": [{"text": "voce e o smaug"}]},
		"contents": [
			{"role": "user", "parts": [{"text": "padaria 12"}]},
			{"role": "model", "parts": [{"functionCall": {"name": "inserir_transacao", "args": {"valor": -12, "descricao": "Padaria"}}}]},
			{"role": "user", "parts": [{"functionResponse": {"name": "inserir_transacao", "response": {"transacoes": [{"id": 42}]}}}]},
			{"role": "user", "parts": [
				{"text": "nota fiscal em anexo"},
				{"inlineData": {"mimeType": "image/jpeg", "data": "ZmFrZWpwZWc="}}
			]}
		],
		"tools": [{"functionDeclarations": [
			{"name": "inserir_transacao", "description": "insere um lancamento", "parameters": {"type": "object", "properties": {"valor": {"type": "number"}}}}
		]}],
		"toolConfig": {"functionCallingConfig": {"mode": "AUTO"}},
		"generationConfig": {"temperature": 0.2, "maxOutputTokens": 1024, "thinkingConfig": {"thinkingBudget": 0}}
	}`

	var got, want map[string]any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("request nao bate.\ngot:  %#v\nwant: %#v", got, want)
	}
}

// TestBuildRequest_InlineDataPNG confere a serializacao de uma Part so com
// inlineData (specs/04 §2.2: imagem inline no turno da nota fiscal) — o
// golden acima ja cobre o jpeg junto de texto; este e o caso isolado pedido
// na fase 5, com mimetype image/png.
func TestBuildRequest_InlineDataPNG(t *testing.T) {
	req := llm.GenerateRequest{
		Model: "gemini-2.5-flash",
		Contents: []llm.Turn{
			{Role: "user", Parts: []llm.Part{{InlineMime: "image/png", InlineData: []byte("abc")}}},
		},
	}

	raw, err := json.Marshal(buildRequest(req))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	contents, ok := got["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("contents = %#v", got["contents"])
	}
	c0, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("contents[0] = %#v", contents[0])
	}
	parts, ok := c0["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("parts = %#v", c0["parts"])
	}
	p0, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("parts[0] = %#v", parts[0])
	}
	inline, ok := p0["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("inlineData ausente: %#v", p0)
	}
	want := map[string]any{"mimeType": "image/png", "data": "YWJj"}
	if !reflect.DeepEqual(inline, want) {
		t.Errorf("inlineData = %#v, want %#v", inline, want)
	}
}

func TestBuildRequest_ThinkingBudgetNil(t *testing.T) {
	req := llm.GenerateRequest{
		Model:        "gemini-2.5-flash",
		Contents:     []llm.Turn{{Role: "user", Parts: []llm.Part{{Text: "oi"}}}},
		Temperature:  0.2,
		MaxOutputTok: 1024,
		// ThinkingBudget nil: nao deve mandar thinkingConfig (vision usa
		// o padrao do modelo).
	}

	raw, err := json.Marshal(buildRequest(req))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	gc, ok := got["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("generationConfig ausente: %#v", got)
	}
	if _, has := gc["thinkingConfig"]; has {
		t.Errorf("thinkingConfig nao deveria aparecer: %#v", gc)
	}
	if _, has := got["tools"]; has {
		t.Errorf("tools nao deveria aparecer sem ToolDecl: %#v", got)
	}
	if _, has := got["system_instruction"]; has {
		t.Errorf("system_instruction nao deveria aparecer sem System: %#v", got)
	}
}
