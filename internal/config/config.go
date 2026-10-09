// Package config lê a configuração do agens do ambiente (.env ou variáveis
// reais), no padrão do Smaug: uma função de lookup injetável, para os testes
// não tocarem em variáveis de ambiente reais. Significado: specs/05 §7.
package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Config reúne tudo que o agens lê do ambiente.
type Config struct {
	Listen    string
	DataDir   string
	AgentsDir string
	TZ        string
	Loc       *time.Location
	LogDebug  bool

	GeminiAPIKey   string
	Model          string
	Temperature    float64
	ThinkingBudget int

	SistemaTimeout time.Duration
	PausaHumanaH   int
	RetencaoDias   int
	ManutencaoHora int
	EncerramentoS  int
	MaxConversas   int
	AdminToken     string // vazio = rota de apagamento desligada
	Transcricao    string // "gemini" ou "groq"
	GroqAPIKey     string

	// Precos: AGENS_PRECO_<MODELO> → "entrada,saida" bruto.
	// DECISÃO: nesta fase fica só como mapa bruto (chave = sufixo após
	// AGENS_PRECO_); o parse e o uso são da fase do llm/preco.go. Preenchido
	// por PrecosBrutos(os.Environ()), pois o lookup não enumera variáveis.
	Precos map[string]string
}

// lookupFunc é o formato de os.LookupEnv.
type lookupFunc func(key string) (string, bool)

func getenvOr(lookup lookupFunc, key, def string) string {
	if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func intEm(lookup lookupFunc, key, def string, min, max int) (int, error) {
	raw := getenvOr(lookup, key, def)
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s inválido (%q): inteiro entre %d e %d", key, raw, min, max)
	}
	return n, nil
}

// LoadConfigFrom monta a Config a partir de uma fonte arbitrária de variáveis.
func LoadConfigFrom(lookup lookupFunc) (*Config, error) {
	cfg := &Config{
		Listen:       getenvOr(lookup, "AGENS_LISTEN", "0.0.0.0:4100"),
		DataDir:      getenvOr(lookup, "AGENS_DATA_DIR", "./data"),
		AgentsDir:    getenvOr(lookup, "AGENS_AGENTES_DIR", "./agentes"),
		TZ:           getenvOr(lookup, "AGENS_TZ", "America/Sao_Paulo"),
		GeminiAPIKey: getenvOr(lookup, "GEMINI_API_KEY", ""),
		Model:        getenvOr(lookup, "AGENS_MODEL", "gemini-2.5-flash"),
		AdminToken:   getenvOr(lookup, "AGENS_ADMIN_TOKEN", ""),
		GroqAPIKey:   getenvOr(lookup, "GROQ_API_KEY", ""),
		Precos:       map[string]string{},
	}

	if cfg.GeminiAPIKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY é obrigatória")
	}
	if _, _, err := net.SplitHostPort(cfg.Listen); err != nil {
		return nil, fmt.Errorf("AGENS_LISTEN inválido (%q): %w", cfg.Listen, err)
	}
	loc, err := time.LoadLocation(cfg.TZ)
	if err != nil {
		return nil, fmt.Errorf("AGENS_TZ inválido (%q): %w", cfg.TZ, err)
	}
	cfg.Loc = loc

	logRaw := getenvOr(lookup, "AGENS_LOG_DEBUG", "false")
	if cfg.LogDebug, err = strconv.ParseBool(logRaw); err != nil {
		return nil, fmt.Errorf("AGENS_LOG_DEBUG inválido (%q)", logRaw)
	}

	tempRaw := getenvOr(lookup, "AGENS_TEMPERATURE", "0")
	if cfg.Temperature, err = strconv.ParseFloat(tempRaw, 64); err != nil || cfg.Temperature < 0 {
		return nil, fmt.Errorf("AGENS_TEMPERATURE inválido (%q)", tempRaw)
	}

	if cfg.ThinkingBudget, err = intEm(lookup, "AGENS_THINKING_BUDGET", "0", 0, 1<<20); err != nil {
		return nil, err
	}

	toRaw := getenvOr(lookup, "AGENS_SISTEMA_TIMEOUT", "10s")
	if cfg.SistemaTimeout, err = time.ParseDuration(toRaw); err != nil || cfg.SistemaTimeout <= 0 {
		return nil, fmt.Errorf("AGENS_SISTEMA_TIMEOUT inválido (%q): duração positiva, ex. 10s", toRaw)
	}

	if cfg.PausaHumanaH, err = intEm(lookup, "AGENS_PAUSA_HUMANA_HORAS", "12", 1, 168); err != nil {
		return nil, err
	}
	if cfg.RetencaoDias, err = intEm(lookup, "AGENS_RETENCAO_DIAS", "90", 1, 3650); err != nil {
		return nil, err
	}
	if cfg.ManutencaoHora, err = intEm(lookup, "AGENS_MANUTENCAO_HORA", "4", 0, 23); err != nil {
		return nil, err
	}
	if cfg.EncerramentoS, err = intEm(lookup, "AGENS_ENCERRAMENTO_S", "30", 1, 600); err != nil {
		return nil, err
	}
	if cfg.MaxConversas, err = intEm(lookup, "AGENS_MAX_CONVERSAS", "1000", 1, 1_000_000); err != nil {
		return nil, err
	}

	cfg.Transcricao = getenvOr(lookup, "AGENS_TRANSCRICAO", "gemini")
	switch cfg.Transcricao {
	case "gemini":
	case "groq":
		if cfg.GroqAPIKey == "" {
			return nil, fmt.Errorf("GROQ_API_KEY é obrigatória com AGENS_TRANSCRICAO=groq")
		}
	default:
		return nil, fmt.Errorf("AGENS_TRANSCRICAO inválido (%q): use gemini ou groq", cfg.Transcricao)
	}
	return cfg, nil
}

// PrecosBrutos extrai AGENS_PRECO_* de uma lista "CHAVE=valor" (os.Environ()).
// A chave do mapa é o sufixo (ex.: "GEMINI_2_5_FLASH"); valor sem parse.
func PrecosBrutos(environ []string) map[string]string {
	const pref = "AGENS_PRECO_"
	m := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(k, pref) && len(k) > len(pref) && strings.TrimSpace(v) != "" {
			m[k[len(pref):]] = strings.TrimSpace(v)
		}
	}
	return m
}
