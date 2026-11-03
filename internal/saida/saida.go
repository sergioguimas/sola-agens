// Package saida cria os clientes HTTP das chamadas de saída (sistema, Gemini,
// Groq). Redirect só é seguido dentro da mesma origem (esquema + host + porta):
// para outro destino o net/http repassa quase todos os headers (ticket do
// cliente, x-goog-api-key, headers fixos), e nenhuma dessas APIs manda o
// cliente para outro host em operação normal. Nesse caso o 3xx volta como
// resposta e cada chamador já o trata como falha.
package saida

import (
	"net/http"
	"time"
)

// maxRedirects é o mesmo limite padrão do net/http.
const maxRedirects = 10

// Cliente devolve um *http.Client que não segue redirect para outra origem.
// timeout 0 = sem timeout global (quem limita é o ctx de cada chamada).
func Cliente(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: mesmaOrigem}
}

func mesmaOrigem(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return http.ErrUseLastResponse
	}
	orig := via[0].URL
	if req.URL.Scheme != orig.Scheme || req.URL.Host != orig.Host {
		return http.ErrUseLastResponse
	}
	return nil
}
