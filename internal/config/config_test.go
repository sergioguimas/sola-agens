package config

import (
	"testing"
	"time"
)

func baseEnv() map[string]string { return map[string]string{"GEMINI_API_KEY": "k"} }

func lookupFrom(env map[string]string) lookupFunc {
	return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
}

func TestDefaults(t *testing.T) {
	cfg, err := LoadConfigFrom(lookupFrom(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		nome      string
		got, want any
	}{
		{"Listen", cfg.Listen, "0.0.0.0:4100"},
		{"DataDir", cfg.DataDir, "./data"},
		{"AgentsDir", cfg.AgentsDir, "./agentes"},
		{"TZ", cfg.TZ, "America/Sao_Paulo"},
		{"LogDebug", cfg.LogDebug, false},
		{"Model", cfg.Model, "gemini-3.6-flash"},
		{"Temperature", cfg.Temperature, 0.0},
		{"ThinkingBudget", cfg.ThinkingBudget, 0},
		{"SistemaTimeout", cfg.SistemaTimeout, 10 * time.Second},
		{"PausaHumanaH", cfg.PausaHumanaH, 12},
		{"RetencaoDias", cfg.RetencaoDias, 90},
		{"ManutencaoHora", cfg.ManutencaoHora, 4},
		{"EncerramentoS", cfg.EncerramentoS, 30},
		{"MaxConversas", cfg.MaxConversas, 1000},
		{"AdminToken", cfg.AdminToken, ""},
		{"Transcricao", cfg.Transcricao, "gemini"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.nome, c.got, c.want)
		}
	}
	if cfg.Loc == nil {
		t.Error("Loc nil")
	}
}

func TestObrigatorias(t *testing.T) {
	if _, err := LoadConfigFrom(lookupFrom(map[string]string{})); err == nil {
		t.Error("sem GEMINI_API_KEY deveria falhar")
	}
	if _, err := LoadConfigFrom(lookupFrom(map[string]string{"GEMINI_API_KEY": "  "})); err == nil {
		t.Error("GEMINI_API_KEY em branco deveria falhar")
	}
	env := baseEnv()
	env["AGENS_TRANSCRICAO"] = "groq"
	if _, err := LoadConfigFrom(lookupFrom(env)); err == nil {
		t.Error("groq sem GROQ_API_KEY deveria falhar")
	}
	env["GROQ_API_KEY"] = "g"
	if cfg, err := LoadConfigFrom(lookupFrom(env)); err != nil || cfg.Transcricao != "groq" {
		t.Errorf("groq com chave: %v", err)
	}
}

func TestInvalidos(t *testing.T) {
	cases := map[string]string{
		"AGENS_LISTEN":             "semporta",
		"AGENS_TZ":                 "Marte/Olympus",
		"AGENS_LOG_DEBUG":          "talvez",
		"AGENS_TEMPERATURE":        "-1",
		"AGENS_THINKING_BUDGET":    "x",
		"AGENS_SISTEMA_TIMEOUT":    "10",
		"AGENS_PAUSA_HUMANA_HORAS": "169",
		"AGENS_RETENCAO_DIAS":      "0",
		"AGENS_MANUTENCAO_HORA":    "24",
		"AGENS_ENCERRAMENTO_S":     "0",
		"AGENS_MAX_CONVERSAS":      "-5",
		"AGENS_TRANSCRICAO":        "whisper",
	}
	for k, v := range cases {
		env := baseEnv()
		env[k] = v
		if _, err := LoadConfigFrom(lookupFrom(env)); err == nil {
			t.Errorf("%s=%q deveria falhar", k, v)
		}
	}
}

func TestPrecosBrutos(t *testing.T) {
	m := PrecosBrutos([]string{"AGENS_PRECO_GEMINI_2_5_FLASH=0.30,2.50", "AGENS_PRECO_=1", "OUTRA=2", "AGENS_PRECO_X="})
	if len(m) != 1 || m["GEMINI_2_5_FLASH"] != "0.30,2.50" {
		t.Errorf("precos = %v", m)
	}
}
