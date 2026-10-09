package transcricao

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// timeoutGroq e o prazo por chamada (specs/04 §2.6).
const timeoutGroq = 90 * time.Second

// backoffsGroq: mesma tabela de espera de internal/llm/gemini/retry.go
// (specs/04 §2.4: 1s, 2s, 4s, 8s — 5 tentativas no total).
//
// DECISÃO: retry.go nao exporta backoffs/retryableStatus/retryAfter/
// sleepFunc (sao detalhe interno do pacote gemini); exportar so para este
// segundo pacote reaproveitar 4 constantes e duas funcoes pequenas parecia
// mais gambiarra que copiar a tabela — que e o que specs/04 §2.6 pede
// explicitamente como plano B.
var backoffsGroq = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// sleepFuncGroq espera d respeitando ctx — injetavel no teste (specs/05
// §9), mesmo desenho de gemini/retry.go.
type sleepFuncGroq func(ctx context.Context, d time.Duration) error

func defaultSleepGroq(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retryableStatusGroq: mesmo criterio de specs/04 §2.4 (429 e 5xx).
func retryableStatusGroq(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

// retryAfterGroq le o header Retry-After (segundos), se presente e valido.
func retryAfterGroq(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// groqTranscritor implementa Transcritor com a API de transcricao do Groq
// (specs/04 §2.6): multipart/form-data cru, sem SDK.
type groqTranscritor struct {
	apiKey     string
	modelo     string
	url        string
	httpClient *http.Client
	sleep      sleepFuncGroq // nil usa defaultSleepGroq; injetavel no teste
}

var _ Transcritor = (*groqTranscritor)(nil)

// Transcrever monta o multipart (file=audio.ogg no mimetype original,
// model, language, response_format=text), autentica com Bearer e tenta de
// novo em 429/5xx/erro de rede com o mesmo backoff do Gemini (specs/04
// §2.4, §2.6). 401/400 nao tentam de novo.
func (g *groqTranscritor) Transcrever(ctx context.Context, audio []byte, mimetype, idioma string) (string, Uso, error) {
	sleep := g.sleep
	if sleep == nil {
		sleep = defaultSleepGroq
	}

	ctx, cancel := context.WithTimeout(ctx, timeoutGroq)
	defer cancel()

	var lastErr error
	totalTentativas := len(backoffsGroq) + 1
	for tentativa := 1; tentativa <= totalTentativas; tentativa++ {
		body, contentType, err := montarMultipart(audio, mimetype, g.modelo, idioma)
		if err != nil {
			return "", Uso{}, fmt.Errorf("transcricao: montar multipart: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, body)
		if err != nil {
			return "", Uso{}, fmt.Errorf("transcricao: montar request: %w", err)
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+g.apiKey)

		resp, doErr := g.httpClient.Do(req)
		if doErr != nil {
			lastErr = doErr
		} else {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return strings.TrimSpace(string(respBody)), Uso{}, nil
			} else if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
				return "", Uso{}, fmt.Errorf("transcricao: groq status %d", resp.StatusCode)
			} else if !retryableStatusGroq(resp.StatusCode) {
				return "", Uso{}, fmt.Errorf("transcricao: groq status %d", resp.StatusCode)
			} else {
				lastErr = fmt.Errorf("status %d", resp.StatusCode)
			}
		}

		if tentativa == totalTentativas {
			break
		}

		wait := backoffsGroq[tentativa-1]
		if resp != nil {
			if ra, ok := retryAfterGroq(resp.Header.Get("Retry-After")); ok {
				wait = ra
			}
		}
		if serr := sleep(ctx, wait); serr != nil {
			return "", Uso{}, fmt.Errorf("transcricao: %w", serr)
		}
	}

	return "", Uso{}, fmt.Errorf("transcricao: groq indisponivel: %w", lastErr)
}

// montarMultipart monta o corpo multipart/form-data da API de transcricao
// do Groq (specs/04 §2.6): file (nome fixo audio.ogg, Content-Type =
// mimetype original), model, language, response_format=text.
func montarMultipart(audio []byte, mimetype, modelo, idioma string) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := createFilePart(w, "file", "audio.ogg", mimetype)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, "", err
	}

	campos := []struct{ nome, valor string }{
		{"model", modelo},
		{"language", idioma},
		{"response_format", "text"},
	}
	for _, c := range campos {
		if err := w.WriteField(c.nome, c.valor); err != nil {
			return nil, "", err
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// createFilePart e multipart.Writer.CreateFormFile, mas com o
// Content-Type do arquivo explicito (CreateFormFile fixa sempre
// application/octet-stream) — o Groq espera o mimetype real do audio.
func createFilePart(w *multipart.Writer, fieldname, filename, contentType string) (io.Writer, error) {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldname, filename))
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return w.CreatePart(h)
}
