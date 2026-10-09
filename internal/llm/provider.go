// Package llm define a interface Provider para modelos de LLM (specs/04
// §2.5) e os tipos de request/response, independentes do provedor
// concreto — hoje gemini, e um dia talvez anthropic (tool use mapeia 1:1
// em Part).
package llm

import (
	"context"
	"errors"
	"fmt"
)

// Provider gera uma resposta a partir de um historico de turnos e um
// conjunto de ferramentas disponiveis (function calling).
type Provider interface {
	Generate(ctx context.Context, req GenerateRequest) (*GenerateResponse, error)
}

// GenerateRequest e o pedido de geracao (specs/04 §2.5).
type GenerateRequest struct {
	Model        string
	System       string
	Contents     []Turn     // memoria + turno atual
	Tools        []ToolDecl // nome, descricao, schema JSON
	Temperature  float64
	MaxOutputTok int
	// ThinkingBudget nil = nao enviar thinkingConfig (o modelo usa o
	// padrao dele — a nota fiscal/vision quer isso). Um valor (mesmo 0)
	// manda thinkingConfig.thinkingBudget explicito (specs/04 §2.2).
	// DECISÃO: ponteiro em vez de valor com sentinela negativo, para
	// distinguir "nao configurado" de "configurado como zero" sem
	// inventar um numero magico.
	ThinkingBudget *int
}

// Turn e um turno da conversa: Role "user" ou "model".
type Turn struct {
	Role  string
	Parts []Part
}

// Part e uma parte de um turno. Exatamente um dos campos deve estar
// preenchido (specs/04 §2.5).
type Part struct {
	Text             string
	InlineMime       string
	InlineData       []byte
	FunctionCall     *FunctionCall
	FunctionResponse *FunctionResponse
	// ThoughtSignature e opaco: os modelos Gemini 2.5 podem devolver uma
	// assinatura de "pensamento" junto de um functionCall e pedem que ela
	// volte no turno seguinte. DECISÃO: campo extra ao spec (04 §2.5),
	// so repassado; outros provedores ignoram.
	ThoughtSignature string
}

// FunctionCall e uma chamada de ferramenta pedida pelo modelo.
type FunctionCall struct {
	Name string
	Args map[string]any
}

// FunctionResponse e o resultado de uma ferramenta, devolvido ao modelo.
type FunctionResponse struct {
	Name     string
	Response map[string]any
}

// ToolDecl declara uma ferramenta disponivel para function calling.
type ToolDecl struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
}

// GenerateResponse e a resposta do modelo (specs/04 §2.3).
type GenerateResponse struct {
	Parts        []Part
	FinishReason string
	TotalTokens  int
	// Tokens de entrada (promptTokenCount) e saída (candidatesTokenCount) da
	// chamada; o agente soma por turno e calcula o custo (preco.go).
	InputTokens  int
	OutputTokens int
}

// Erros sentinela de Generate (specs/04 §2.4).
var (
	// ErrLLMIndisponivel: esgotaram-se as tentativas de retry (429/5xx/
	// timeout/erro de rede persistente).
	ErrLLMIndisponivel = errors.New("llm indisponivel")
	// ErrRespostaVazia: candidates vazio ou sem content (specs/04 §2.3).
	ErrRespostaVazia = errors.New("llm: resposta vazia")
)

// ErrLLMDefinitivo e um erro que nao vale a pena tentar de novo (400
// request invalido, 403 chave). Msg vem de error.message da resposta da
// API — conteudo de erro do provedor, nao do usuario, entao pode ir ao
// log (specs/04 §2.4).
type ErrLLMDefinitivo struct {
	Status int
	Msg    string
}

func (e *ErrLLMDefinitivo) Error() string {
	return fmt.Sprintf("llm: erro definitivo (status %d): %s", e.Status, e.Msg)
}

// ErrFinishReason e devolvido quando o modelo termina por um motivo que
// nao e uma resposta normal (specs/04 §2.3: MAX_TOKENS, SAFETY,
// RECITATION, OTHER).
type ErrFinishReason struct {
	Reason string
}

func (e *ErrFinishReason) Error() string {
	return fmt.Sprintf("llm: finishReason=%s", e.Reason)
}
