// Package gemini implementa llm.Provider chamando a API REST do Gemini
// diretamente (sem SDK — specs/04 §2). A pessoa que mexer aqui depois deve
// reconferir os nomes de campo na doc oficial (ai.google.dev/api/generate-content):
// este pacote reflete a forma da API v1beta em vigor em 2026.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"sola-agens/internal/llm"
	"sola-agens/internal/saida"
)

// defaultBaseURL e o endpoint padrao da API Gemini v1beta (specs/04 §2.1).
const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// maxResposta limita o corpo lido de uma resposta: texto e chamadas de
// ferramenta cabem com folga em 8 MB; acima disso a resposta e recusada em vez
// de ocupar memoria sem limite.
const maxResposta = 8 << 20

// errRespostaGrande e devolvido quando o corpo passa de maxResposta.
var errRespostaGrande = errors.New("gemini: resposta grande demais")

// Client fala REST cru com a API Gemini.
type Client struct {
	// BaseURL e exportado para o teste apontar para um httptest.Server.
	BaseURL string

	apiKey     string
	httpClient *http.Client
	logger     *slog.Logger
	sleep      sleepFunc // injetavel no teste de retry
}

var _ llm.Provider = (*Client)(nil)

// NewClient cria um cliente Gemini. httpClient nil usa saida.Cliente(0)
// (sem redirect e sem timeout global — quem limita e o ctx de cada chamada,
// specs/04 §2.4).
func NewClient(apiKey string, httpClient *http.Client, logger *slog.Logger) *Client {
	if httpClient == nil {
		httpClient = saida.Cliente(0)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		BaseURL:    defaultBaseURL,
		apiKey:     apiKey,
		httpClient: httpClient,
		logger:     logger,
		sleep:      defaultSleep,
	}
}

// attemptResult e o resultado bruto de uma tentativa HTTP.
type attemptResult struct {
	status     int
	body       []byte
	retryAfter string
}

func (c *Client) doOnce(ctx context.Context, url string, raw []byte) (attemptResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return attemptResult{}, fmt.Errorf("gemini: montar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return attemptResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResposta+1))
	if err != nil {
		return attemptResult{status: resp.StatusCode}, err
	}
	// So o 2xx e usado inteiro; num erro o corpo truncado basta para a mensagem
	// e o status segue decidindo o retry.
	if len(body) > maxResposta {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return attemptResult{status: resp.StatusCode}, errRespostaGrande
		}
		body = body[:maxResposta]
	}
	return attemptResult{status: resp.StatusCode, body: body, retryAfter: resp.Header.Get("Retry-After")}, nil
}

// Generate implementa llm.Provider (specs/04 §2). Nao fixa timeout
// proprio: respeita o ctx que o chamador passa (60s texto / 90s imagem,
// specs/04 §2.4) — inclusive entre tentativas de retry.
func (c *Client) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	wireReq := buildRequest(req)
	raw, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: montar request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", c.BaseURL, req.Model)

	var lastErr error
	totalTentativas := len(backoffs) + 1
	for tentativa := 1; tentativa <= totalTentativas; tentativa++ {
		start := time.Now()
		res, doErr := c.doOnce(ctx, url, raw)
		ms := time.Since(start).Milliseconds()

		if doErr == nil && res.status >= 200 && res.status < 300 {
			resp, tokens, perr := parseResponse(res.body)
			c.logger.Info("llm", "evt", "llm", "modelo", req.Model, "status", res.status, "tentativa", tentativa, "tokens", tokens, "ms", ms)
			if perr != nil {
				return nil, perr
			}
			return resp, nil
		}

		c.logger.Info("llm", "evt", "llm", "modelo", req.Model, "status", res.status, "tentativa", tentativa, "ms", ms)

		if doErr == nil && (res.status == http.StatusBadRequest || res.status == http.StatusForbidden) {
			msg := errorMessage(res.body)
			c.logger.Error("llm", "evt", "llm", "modelo", req.Model, "status", res.status, "erro", msg)
			return nil, &llm.ErrLLMDefinitivo{Status: res.status, Msg: msg}
		}

		// Resposta grande demais nao melhora tentando de novo, e cada tentativa custa.
		if errors.Is(doErr, errRespostaGrande) {
			return nil, fmt.Errorf("%w: %w", llm.ErrLLMIndisponivel, doErr)
		}

		retryable := doErr != nil || retryableStatus(res.status)
		if !retryable {
			return nil, fmt.Errorf("%w: status %d", llm.ErrLLMIndisponivel, res.status)
		}

		lastErr = doErr
		if lastErr == nil {
			lastErr = fmt.Errorf("status %d", res.status)
		}

		if tentativa == totalTentativas {
			break // ultima tentativa: nao espera mais, so sai do loop
		}

		wait := backoffs[tentativa-1]
		if ra, ok := retryAfter(res.retryAfter); ok {
			wait = ra
		}
		if serr := c.sleep(ctx, wait); serr != nil {
			return nil, fmt.Errorf("%w: %v", llm.ErrLLMIndisponivel, serr)
		}
	}

	return nil, fmt.Errorf("%w: %v", llm.ErrLLMIndisponivel, lastErr)
}
