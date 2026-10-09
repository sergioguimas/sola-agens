package gemini

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"sola-agens/internal/llm"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeSleeper grava as duracoes pedidas e nao espera de verdade.
type fakeSleeper struct {
	mu     sync.Mutex
	waited []time.Duration
}

func (f *fakeSleeper) sleep(_ context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waited = append(f.waited, d)
	return nil
}

const okBody = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":10}}`

func TestGenerate_RetryAte200(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := NewClient("chave", nil, nopLogger())
	c.BaseURL = srv.URL
	fs := &fakeSleeper{}
	c.sleep = fs.sleep

	resp, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "m", Contents: []llm.Turn{{Role: "user", Parts: []llm.Part{{Text: "oi"}}}}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if len(fs.waited) != 2 || fs.waited[0] != 1*time.Second || fs.waited[1] != 2*time.Second {
		t.Errorf("waited = %v, want [1s 2s]", fs.waited)
	}
	if len(resp.Parts) != 1 || resp.Parts[0].Text != "ok" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestGenerate_EsgotaTentativas(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClient("chave", nil, nopLogger())
	c.BaseURL = srv.URL
	fs := &fakeSleeper{}
	c.sleep = fs.sleep

	_, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "m"})
	if err == nil {
		t.Fatal("esperava erro")
	}
	if calls != 5 {
		t.Fatalf("calls = %d, want 5 (1 + 4 retries)", calls)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(fs.waited) != len(want) {
		t.Fatalf("waited = %v, want %v", fs.waited, want)
	}
	for i, w := range want {
		if fs.waited[i] != w {
			t.Errorf("waited[%d] = %v, want %v", i, fs.waited[i], w)
		}
	}
}

func TestGenerate_400SemRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"request invalido: campo x ausente"}}`))
	}))
	defer srv.Close()

	c := NewClient("chave", nil, nopLogger())
	c.BaseURL = srv.URL
	fs := &fakeSleeper{}
	c.sleep = fs.sleep

	_, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "m"})
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (sem retry)", calls)
	}
	def, ok := err.(*llm.ErrLLMDefinitivo)
	if !ok {
		t.Fatalf("err = %v (%T), want *ErrLLMDefinitivo", err, err)
	}
	if def.Status != 400 || def.Msg != "request invalido: campo x ausente" {
		t.Errorf("def = %+v", def)
	}
	if len(fs.waited) != 0 {
		t.Errorf("nao deveria ter esperado: %v", fs.waited)
	}
}

func TestGenerate_403SemRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer srv.Close()

	c := NewClient("chave-errada", nil, nopLogger())
	c.BaseURL = srv.URL
	c.sleep = (&fakeSleeper{}).sleep

	_, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "m"})
	def, ok := err.(*llm.ErrLLMDefinitivo)
	if !ok || def.Status != 403 {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerate_RetryAfterRespeitado(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := NewClient("chave", nil, nopLogger())
	c.BaseURL = srv.URL
	fs := &fakeSleeper{}
	c.sleep = fs.sleep

	if _, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "m"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(fs.waited) != 1 || fs.waited[0] != 7*time.Second {
		t.Errorf("waited = %v, want [7s] (Retry-After deveria sobrepor o backoff padrao de 1s)", fs.waited)
	}
}

func TestGenerate_TimeoutViaCtxCancelado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	c := NewClient("chave", nil, nopLogger())
	c.BaseURL = srv.URL
	c.sleep = (&fakeSleeper{}).sleep

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ja cancelado antes de chamar

	_, err := c.Generate(ctx, llm.GenerateRequest{Model: "m"})
	if err == nil {
		t.Fatal("esperava erro por ctx cancelado")
	}
}
