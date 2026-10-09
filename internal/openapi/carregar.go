package openapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"sola-agens/internal/agentes"
)

// esperasBoot são as pausas entre as 5 tentativas de baixar a spec (specs/02 §1).
var esperasBoot = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second}

// maxSpec limita o tamanho da spec aceita.
const maxSpec = 16 << 20

type esperaFunc func(ctx context.Context, d time.Duration) error

func esperaReal(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Carregar baixa a OpenAPI do sistema (GET base+openapi_path com o token
// Bearer). Status ≠ 200 ou JSON inválido contam como falha; 5 tentativas com
// espera de 2/4/8/16/32 s entre elas.
func Carregar(ctx context.Context, cli *http.Client, ag agentes.Agente) ([]byte, error) {
	return carregar(ctx, cli, ag.BaseURL()+ag.Sistema.OpenAPIPath, ag.Token(), esperasBoot, esperaReal)
}

func carregar(ctx context.Context, cli *http.Client, url, token string, esperas []time.Duration, espera esperaFunc) ([]byte, error) {
	if cli == nil {
		cli = http.DefaultClient
	}
	var ultimo error
	for i := 0; i < len(esperas); i++ { // DECISÃO: 5 tentativas; a espera de 32 s só existiria depois da última
		if i > 0 {
			if err := espera(ctx, esperas[i-1]); err != nil {
				return nil, err
			}
		}
		b, err := baixar(ctx, cli, url, token)
		if err == nil {
			return b, nil
		}
		ultimo = err
	}
	return nil, fmt.Errorf("openapi: %d tentativas falharam: %w", len(esperas), ultimo)
}

// baixar faz uma tentativa. Os erros não incluem a URL com segredos (não há) nem
// o corpo da resposta.
func baixar(ctx context.Context, cli *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, errors.New("falha de rede ao baixar a spec")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d ao baixar a spec", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSpec+1))
	if err != nil {
		return nil, errors.New("falha ao ler a spec")
	}
	if len(b) > maxSpec {
		return nil, errors.New("spec grande demais")
	}
	if !json.Valid(b) {
		return nil, errors.New("spec não é JSON válido")
	}
	return b, nil
}
