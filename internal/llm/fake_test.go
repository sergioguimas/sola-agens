package llm

import (
	"context"
	"errors"
	"testing"
)

func TestFake_Sequencia(t *testing.T) {
	f := &Fake{Respostas: []GenerateResponse{
		CallResp("inserir_transacao", map[string]any{"valor": -12.0}),
		TextoResp("Lançado."),
	}}

	r1, err := f.Generate(context.Background(), GenerateRequest{Model: "m1"})
	if err != nil {
		t.Fatalf("1a chamada: %v", err)
	}
	if len(r1.Parts) != 1 || r1.Parts[0].FunctionCall == nil || r1.Parts[0].FunctionCall.Name != "inserir_transacao" {
		t.Errorf("r1 = %+v", r1)
	}

	r2, err := f.Generate(context.Background(), GenerateRequest{Model: "m1"})
	if err != nil {
		t.Fatalf("2a chamada: %v", err)
	}
	if len(r2.Parts) != 1 || r2.Parts[0].Text != "Lançado." {
		t.Errorf("r2 = %+v", r2)
	}

	if len(f.Reqs) != 2 {
		t.Fatalf("Reqs = %d, want 2", len(f.Reqs))
	}

	if _, err := f.Generate(context.Background(), GenerateRequest{}); err == nil {
		t.Fatal("esperava erro de roteiro esgotado")
	}
}

func TestFake_Err(t *testing.T) {
	f := &Fake{Err: errors.New("indisponivel")}
	if _, err := f.Generate(context.Background(), GenerateRequest{}); err == nil {
		t.Fatal("esperava erro")
	}
	if len(f.Reqs) != 1 {
		t.Errorf("Reqs = %d, want 1 (deve gravar mesmo com erro)", len(f.Reqs))
	}
}
