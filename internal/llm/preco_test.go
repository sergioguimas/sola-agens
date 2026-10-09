package llm

import "testing"

func TestParsePreco(t *testing.T) {
	cases := []struct {
		in      string
		want    preco
		wantErr bool
	}{
		{"0.30,2.50", preco{0.30, 2.50}, false},
		{" 1 , 4 ", preco{1, 4}, false},
		{"1", preco{}, true},
		{"a,b", preco{}, true},
		{"-1,2", preco{}, true},
		{"", preco{}, true},
	}
	for _, c := range cases {
		got, err := parsePreco(c.in)
		if (err != nil) != c.wantErr || (!c.wantErr && got != c.want) {
			t.Errorf("parsePreco(%q) = %v, %v", c.in, got, err)
		}
	}
}

func TestChavePreco(t *testing.T) {
	if got := ChavePreco("gemini-2.5-flash-lite"); got != "GEMINI_2_5_FLASH_LITE" {
		t.Errorf("ChavePreco = %q", got)
	}
}

func TestCusto(t *testing.T) {
	t.Cleanup(func() { _ = ConfigurarPrecos(nil) })
	_ = ConfigurarPrecos(nil)
	cases := []struct {
		nome    string
		modelo  string
		in, out int
		brutos  map[string]string
		want    int64
	}{
		{"flash padrão", "gemini-2.5-flash", 1_000_000, 1_000_000, nil, 2_800_000},
		{"flash-lite", "gemini-2.5-flash-lite", 2000, 100, nil, 240},
		{"zero tokens", "gemini-2.5-flash", 0, 0, nil, 0},
		{"desconhecido", "modelo-x", 1000, 1000, nil, 0},
		{"sobrescrito", "gemini-2.5-flash", 1000, 1000, map[string]string{"GEMINI_2_5_FLASH": "1,2"}, 3000},
		{"novo modelo por env", "modelo-x", 1000, 0, map[string]string{"MODELO_X": "5,6"}, 5000},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			if err := ConfigurarPrecos(c.brutos); err != nil {
				t.Fatal(err)
			}
			if got := Custo(c.modelo, c.in, c.out); got != c.want {
				t.Errorf("Custo = %d, want %d", got, c.want)
			}
		})
	}
}

func TestConfigurarPrecos_Invalido(t *testing.T) {
	if err := ConfigurarPrecos(map[string]string{"X": "abc"}); err == nil {
		t.Error("esperava erro")
	}
	_ = ConfigurarPrecos(nil)
}
