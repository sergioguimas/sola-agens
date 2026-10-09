package transcricao

import (
	"testing"

	"sola-agens/internal/llm"
)

func TestNew(t *testing.T) {
	t.Run("gemini", func(t *testing.T) {
		tr, err := New(Config{Provedor: "gemini", ModeloGemini: "gemini-test"}, &llm.Fake{}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, ok := tr.(*geminiTranscritor); !ok {
			t.Fatalf("tipo = %T, want *geminiTranscritor", tr)
		}
	})

	t.Run("groq", func(t *testing.T) {
		tr, err := New(Config{Provedor: "groq", GroqKey: "k", GroqModel: "whisper"}, nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		g, ok := tr.(*groqTranscritor)
		if !ok {
			t.Fatalf("tipo = %T, want *groqTranscritor", tr)
		}
		if g.url != groqDefaultURL {
			t.Errorf("url = %q, want default %q", g.url, groqDefaultURL)
		}
		if g.httpClient == nil {
			t.Error("httpClient nil, want default *http.Client{}")
		}
	})

	t.Run("groq com URL customizada", func(t *testing.T) {
		tr, err := New(Config{Provedor: "groq", GroqKey: "k", GroqURL: "http://exemplo/x"}, nil, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		g := tr.(*groqTranscritor)
		if g.url != "http://exemplo/x" {
			t.Errorf("url = %q, want customizada", g.url)
		}
	})

	t.Run("provedor desconhecido -> erro", func(t *testing.T) {
		if _, err := New(Config{Provedor: "whisper-cloud"}, nil, nil); err == nil {
			t.Fatal("esperava erro")
		}
	})
}
