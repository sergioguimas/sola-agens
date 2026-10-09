package llm

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
)

// preco é o valor em US$ por milhão de tokens.
type preco struct{ Entrada, Saida float64 }

// Valores iniciais (US$ por milhão de tokens). Conferir na página de preços do
// Gemini antes do deploy; AGENS_PRECO_<MODELO> sobrescreve sem recompilar.
var precosPadrao = map[string]preco{
	"gemini-2.5-flash":      {0.30, 2.50},
	"gemini-2.5-flash-lite": {0.10, 0.40},
}

var (
	mu          sync.RWMutex
	sobrescrito = map[string]preco{} // chave = ChavePreco(modelo)
	avisados    = map[string]bool{}
)

// ChavePreco devolve o sufixo de AGENS_PRECO_<MODELO>: maiúsculas, '-' e '.'
// trocados por '_' (gemini-2.5-flash → GEMINI_2_5_FLASH).
func ChavePreco(modelo string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToUpper(modelo))
}

// parsePreco lê "entrada,saida" (decimais com ponto).
func parsePreco(v string) (preco, error) {
	a, b, ok := strings.Cut(v, ",")
	if !ok {
		return preco{}, fmt.Errorf("use \"entrada,saida\"")
	}
	e, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	s, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil || e < 0 || s < 0 || math.IsNaN(e) || math.IsNaN(s) || math.IsInf(e, 0) || math.IsInf(s, 0) {
		return preco{}, fmt.Errorf("valores inválidos em %q", v)
	}
	return preco{e, s}, nil
}

// ConfigurarPrecos aplica o mapa bruto de Config.Precos (chave = sufixo de
// AGENS_PRECO_*). Valor inválido = erro (o boot deve falhar). Substitui
// sobrescritas anteriores.
func ConfigurarPrecos(brutos map[string]string) error {
	novo := make(map[string]preco, len(brutos))
	for k, v := range brutos {
		p, err := parsePreco(v)
		if err != nil {
			return fmt.Errorf("AGENS_PRECO_%s: %w", k, err)
		}
		novo[strings.ToUpper(k)] = p
	}
	mu.Lock()
	sobrescrito = novo
	mu.Unlock()
	return nil
}

// Custo estima o custo da chamada em micro-US$ (US$ 1 = 1.000.000). Modelo
// sem preço conhecido devolve 0 e avisa uma vez no log.
func Custo(modelo string, in, out int) int64 {
	mu.RLock()
	p, ok := sobrescrito[ChavePreco(modelo)]
	mu.RUnlock()
	if !ok {
		p, ok = precosPadrao[modelo]
	}
	if !ok {
		mu.Lock()
		novo := !avisados[modelo]
		avisados[modelo] = true
		mu.Unlock()
		if novo {
			slog.Warn("llm: modelo sem preço conhecido; custo estimado = 0", "modelo", modelo)
		}
		return 0
	}
	// tokens × (US$/M) = micro-US$ exatos.
	return int64(math.Round(float64(in)*p.Entrada + float64(out)*p.Saida))
}
