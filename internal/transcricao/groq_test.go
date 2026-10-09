package transcricao

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSleeperGroq grava as duracoes pedidas e nao espera de verdade —
// mesmo desenho de internal/llm/gemini/client_test.go.
type fakeSleeperGroq struct {
	mu     sync.Mutex
	waited []time.Duration
}

func (f *fakeSleeperGroq) sleep(_ context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waited = append(f.waited, d)
	return nil
}

// TestGroqTranscritor_MultipartEHeaders valida o corpo multipart e os
// headers do request (specs/04 §2.6): file (nome audio.ogg, mimetype
// original), model, language, response_format=text, Authorization Bearer.
func TestGroqTranscritor_MultipartEHeaders(t *testing.T) {
	var gotAuth, gotModel, gotLanguage, gotResponseFormat, gotFilename, gotFileContentType string
	var gotFileBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		gotModel = r.FormValue("model")
		gotLanguage = r.FormValue("language")
		gotResponseFormat = r.FormValue("response_format")

		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		gotFilename = header.Filename
		gotFileContentType = header.Header.Get("Content-Type")
		gotFileBytes, _ = io.ReadAll(file)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("  transcrito com espaços em volta  \n"))
	}))
	defer srv.Close()

	tr := &groqTranscritor{apiKey: "chave-groq", modelo: "whisper-large-v3-turbo", url: srv.URL, httpClient: srv.Client()}

	audio := []byte{0xAA, 0xBB, 0xCC}
	texto, _, err := tr.Transcrever(context.Background(), audio, "audio/ogg", "pt")
	if err != nil {
		t.Fatalf("Transcrever: %v", err)
	}
	if texto != "transcrito com espaços em volta" {
		t.Fatalf("texto = %q, want trimado", texto)
	}

	if gotAuth != "Bearer chave-groq" {
		t.Errorf("Authorization = %q, want Bearer chave-groq", gotAuth)
	}
	if gotModel != "whisper-large-v3-turbo" {
		t.Errorf("model = %q", gotModel)
	}
	if gotLanguage != "pt" {
		t.Errorf("language = %q, want pt", gotLanguage)
	}
	if gotResponseFormat != "text" {
		t.Errorf("response_format = %q, want text", gotResponseFormat)
	}
	if gotFilename != "audio.ogg" {
		t.Errorf("filename = %q, want audio.ogg", gotFilename)
	}
	if gotFileContentType != "audio/ogg" {
		t.Errorf("file Content-Type = %q, want audio/ogg", gotFileContentType)
	}
	if string(gotFileBytes) != string(audio) {
		t.Errorf("bytes do arquivo = %v, want %v", gotFileBytes, audio)
	}
}

// TestGroqTranscritor_RetryEm429 cobre 429 seguido de 200 (specs/04 §2.4,
// §2.6): a chamada tenta de novo e devolve o texto da segunda tentativa.
func TestGroqTranscritor_RetryEm429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	fs := &fakeSleeperGroq{}
	tr := &groqTranscritor{apiKey: "k", modelo: "m", url: srv.URL, httpClient: srv.Client(), sleep: fs.sleep}

	texto, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err != nil {
		t.Fatalf("Transcrever: %v", err)
	}
	if texto != "ok" {
		t.Fatalf("texto = %q, want ok", texto)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if len(fs.waited) != 1 || fs.waited[0] != 1*time.Second {
		t.Errorf("waited = %v, want [1s]", fs.waited)
	}
}

// TestGroqTranscritor_401SemRetry: erro de autenticacao e definitivo, nao
// tenta de novo (specs/04 §2.4, §2.6).
func TestGroqTranscritor_401SemRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	fs := &fakeSleeperGroq{}
	tr := &groqTranscritor{apiKey: "k-invalida", modelo: "m", url: srv.URL, httpClient: srv.Client(), sleep: fs.sleep}

	_, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err == nil {
		t.Fatal("esperava erro")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (sem retry)", calls)
	}
	if len(fs.waited) != 0 {
		t.Errorf("waited = %v, want nada (sem retry)", fs.waited)
	}
}

// TestGroqTranscritor_400SemRetry: request invalido tambem e definitivo.
func TestGroqTranscritor_400SemRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	tr := &groqTranscritor{apiKey: "k", modelo: "m", url: srv.URL, httpClient: srv.Client(), sleep: (&fakeSleeperGroq{}).sleep}

	_, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err == nil {
		t.Fatal("esperava erro")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (sem retry)", calls)
	}
}

// TestGroqTranscritor_EsgotaTentativas: 503 persistente esgota as 5
// tentativas (1 + 4 retries) e devolve erro.
func TestGroqTranscritor_EsgotaTentativas(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	fs := &fakeSleeperGroq{}
	tr := &groqTranscritor{apiKey: "k", modelo: "m", url: srv.URL, httpClient: srv.Client(), sleep: fs.sleep}

	_, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err == nil {
		t.Fatal("esperava erro")
	}
	if calls != 5 {
		t.Fatalf("calls = %d, want 5", calls)
	}
	if len(fs.waited) != 4 {
		t.Fatalf("waited = %v, want 4 esperas", fs.waited)
	}
}

func TestGroqTranscritor_RespeitaRetryAfter(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	fs := &fakeSleeperGroq{}
	tr := &groqTranscritor{apiKey: "k", modelo: "m", url: srv.URL, httpClient: srv.Client(), sleep: fs.sleep}

	if _, _, err := tr.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt"); err != nil {
		t.Fatalf("Transcrever: %v", err)
	}
	if len(fs.waited) != 1 || fs.waited[0] != 3*time.Second {
		t.Errorf("waited = %v, want [3s] (Retry-After)", fs.waited)
	}
}

func TestMontarMultipart_ContentTypeVazioNaoQuebra(t *testing.T) {
	buf, contentType, err := montarMultipart([]byte{0x01}, "", "m", "pt")
	if err != nil {
		t.Fatalf("montarMultipart: %v", err)
	}
	if !strings.Contains(contentType, "multipart/form-data") {
		t.Errorf("contentType = %q", contentType)
	}
	if buf.Len() == 0 {
		t.Error("corpo vazio")
	}
}
