package gemini

import (
	"context"
	"strconv"
	"time"
)

// sleepFunc espera d respeitando ctx (devolve ctx.Err() se cancelado antes
// de terminar). Injetavel nos testes para nao esperar de verdade
// (specs/05 §9).
type sleepFunc func(ctx context.Context, d time.Duration) error

func defaultSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// backoffs entre tentativas (specs/04 §2.4): 1s, 2s, 4s, 8s — 5 tentativas
// no total (a primeira chamada + 4 retries, como o maxTries:5 do MVP).
var backoffs = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// retryAfter le o header Retry-After (em segundos, formato usado pela API
// do Google) se presente e valido.
func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

// retryableStatus indica se um status HTTP vale a pena tentar de novo
// (specs/04 §2.4).
func retryableStatus(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}
