package llm

import (
	"context"
	"fmt"
	"sync"
)

// Fake e um Provider roteirizado para testes sem rede (specs/05 §9):
// devolve Respostas em sequencia (uma por chamada a Generate) e grava cada
// request recebido em Reqs, para o teste inspecionar. Zero value ja
// funciona; thread-safe.
type Fake struct {
	mu sync.Mutex

	Respostas []GenerateResponse
	Reqs      []GenerateRequest
	Err       error // se != nil, toda chamada devolve esse erro

	proximo int
}

var _ Provider = (*Fake)(nil)

// Generate grava req em Reqs e devolve a proxima resposta do roteiro. Sem
// Err setado e sem respostas restantes, devolve um erro claro (nao
// silencioso) para o teste apontar o roteiro incompleto.
func (f *Fake) Generate(_ context.Context, req GenerateRequest) (*GenerateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Reqs = append(f.Reqs, req)

	if f.Err != nil {
		return nil, f.Err
	}
	if f.proximo >= len(f.Respostas) {
		return nil, fmt.Errorf("llm.Fake: sem resposta roteirizada para a chamada %d", f.proximo+1)
	}
	resp := f.Respostas[f.proximo]
	f.proximo++
	return &resp, nil
}

// TextoResp monta uma GenerateResponse de texto simples, finishReason
// STOP — o caso "modelo respondeu, sem chamar ferramenta".
func TextoResp(s string) GenerateResponse {
	return GenerateResponse{
		Parts:        []Part{{Text: s}},
		FinishReason: "STOP",
	}
}

// CallResp monta uma GenerateResponse com uma unica chamada de ferramenta.
// Para chamadas paralelas, monte a GenerateResponse a mao com varias Parts.
func CallResp(name string, args map[string]any) GenerateResponse {
	return GenerateResponse{
		Parts:        []Part{{FunctionCall: &FunctionCall{Name: name, Args: args}}},
		FinishReason: "STOP",
	}
}
