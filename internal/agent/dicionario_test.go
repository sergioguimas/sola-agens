package agent

import (
	"reflect"
	"strings"
	"testing"

	"sola-agens/internal/sistema"
)

func sp(s string) *string { return &s }

func sistemaIdentificacaoDesconhecida() sistema.Identificacao {
	return sistema.Identificacao{Situacao: sistema.SituacaoDesconhecido}
}

func TestNormalizar(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Sim!", "sim"},
		{"  OK.  ", "ok"},
		{"Tá   bom", "ta bom"},
		{"CONFIRMÁÁÁ!!!", "confirmaaa"},
		{"...pode ser?!", "pode ser"},
		{"Não vou", "nao vou"},
		{"👍", "👍"},
		{"👍🏽", "👍"},
		{"👍!", "👍"},
		{"¡Claro!", "claro"},
		{"ação", "acao"},
		{"", ""},
		{"  \n\t ", ""},
		{"sim, claro", "sim, claro"}, // pontuação interna fica: não bate com "sim" nem com "claro"
	} {
		if got := Normalizar(c.in); got != c.want {
			t.Errorf("Normalizar(%q) = %q, quer %q", c.in, got, c.want)
		}
	}
}

func TestPadraoTemOsSeteTextosEAsPalavras(t *testing.T) {
	d := Padrao()
	r := d.Respostas
	for nome, v := range map[string]string{"confirmado": r.Confirmado, "cancelado": r.Cancelado, "falhaTecnica": r.FalhaTecnica,
		"midiaNaoSuportada": r.MidiaNaoSuportada, "audioLongo": r.AudioLongo, "naoSuportado": r.NaoSuportado, "equipeSemContato": r.EquipeSemContato} {
		if strings.TrimSpace(v) == "" {
			t.Errorf("resposta %s vazia", nome)
		}
	}
	if r.NaoSuportado != r.MidiaNaoSuportada {
		t.Error("naoSuportado deve ser igual a midiaNaoSuportada")
	}
	if !contemPalavra(d.Palavras.Confirmar, "ta bom") || !contemPalavra(d.Palavras.Confirmar, "👍") ||
		!contemPalavra(d.Palavras.Cancelar, "nao vou") {
		t.Error("palavras padrão incompletas")
	}
	// fora da lista de cancelar, de propósito (specs/02 §5)
	for _, p := range []string{"nao", "remarcar", "outro dia", "infelizmente", "impossivel"} {
		if contemPalavra(d.Palavras.Cancelar, p) {
			t.Errorf("%q não deveria cancelar", p)
		}
	}
}

func TestMesclar(t *testing.T) {
	horas := 4
	zero := 0
	cenarios := []struct {
		nome string
		in   sistema.Atendente
		fn   func(t *testing.T, d Dicionario)
	}{
		{"vazio = padrão", sistema.Atendente{}, func(t *testing.T, d Dicionario) {
			if !reflect.DeepEqual(d, Padrao()) {
				t.Errorf("dicionário mudou: %+v", d)
			}
		}},
		{"nil e vazio e só espaços = padrão", sistema.Atendente{
			Persona: sp(""), Tom: sp("   "), PausaHumanaHoras: &zero,
			Respostas: sistema.Respostas{Confirmado: sp(""), Cancelado: nil},
			Palavras:  sistema.Palavras{Confirmar: []string{}, Cancelar: []string{" ", ""}},
		}, func(t *testing.T, d Dicionario) {
			if !reflect.DeepEqual(d, Padrao()) {
				t.Errorf("dicionário mudou: %+v", d)
			}
		}},
		{"campo a campo", sistema.Atendente{
			Persona: sp(" Lia "), Tom: sp("animado"), PausaHumanaHoras: &horas,
			Respostas: sistema.Respostas{Confirmado: sp("Fechado!"), MidiaNaoSuportada: sp("Só texto.")},
			Palavras:  sistema.Palavras{Confirmar: []string{"fechou", " ", "bora"}},
		}, func(t *testing.T, d Dicionario) {
			p := Padrao()
			if d.Persona != "Lia" || d.Tom != "animado" || d.PausaHumanaHoras != 4 {
				t.Errorf("persona/tom/pausa = %q/%q/%d", d.Persona, d.Tom, d.PausaHumanaHoras)
			}
			if d.Respostas.Confirmado != "Fechado!" || d.Respostas.Cancelado != p.Respostas.Cancelado ||
				d.Respostas.FalhaTecnica != p.Respostas.FalhaTecnica {
				t.Errorf("respostas = %+v", d.Respostas)
			}
			if d.Respostas.MidiaNaoSuportada != "Só texto." || d.Respostas.NaoSuportado != "Só texto." {
				t.Errorf("naoSuportado deve acompanhar midiaNaoSuportada: %+v", d.Respostas)
			}
			if !reflect.DeepEqual(d.Palavras.Confirmar, []string{"fechou", "bora"}) || !reflect.DeepEqual(d.Palavras.Cancelar, p.Palavras.Cancelar) {
				t.Errorf("palavras = %+v", d.Palavras)
			}
		}},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) { c.fn(t, Mesclar(Padrao(), c.in)) })
	}
}

func TestMesclarNaoAlteraOPadrao(t *testing.T) {
	_ = Mesclar(Padrao(), sistema.Atendente{Palavras: sistema.Palavras{Confirmar: []string{"x"}}})
	if !contemPalavra(Padrao().Palavras.Confirmar, "sim") {
		t.Error("o padrão foi alterado")
	}
}

func TestAplicarPrimeiroNome(t *testing.T) {
	for _, c := range []struct{ texto, nome, want string }{
		{"Prontinho, {primeiroNome}! Seu horário está confirmado.", "Ana", "Prontinho, Ana! Seu horário está confirmado."},
		{"Prontinho, {primeiroNome}! Seu horário está confirmado.", "", "Prontinho! Seu horário está confirmado."},
		{"Prontinho, {primeiroNome}! Seu horário está confirmado.", "   ", "Prontinho! Seu horário está confirmado."},
		{"Oi {primeiroNome}!", "", "Oi!"},
		{"Oi {primeiroNome}, tudo bem?", "", "Oi, tudo bem?"},
		{"{primeiroNome}, seu horário está confirmado.", "", "Seu horário está confirmado."},
		{"{primeiroNome} seu horário", "", "Seu horário"},
		{"Olá, {primeiroNome}, tudo certo?", "", "Olá, tudo certo?"},
		{"Tudo certo, {primeiroNome}.", "", "Tudo certo."},
		{"{primeiroNome} e {primeiroNome}", "Zé", "Zé e Zé"},
		{"Sem placeholder.", "", "Sem placeholder."},
		{"Sem placeholder.", "Ana", "Sem placeholder."},
	} {
		if got := Aplicar(c.texto, c.nome); got != c.want {
			t.Errorf("Aplicar(%q, %q) = %q, quer %q", c.texto, c.nome, got, c.want)
		}
	}
}

func TestDividir(t *testing.T) {
	long := func(c string, n int) string { return strings.Repeat(c, n) }
	cenarios := []struct {
		nome string
		in   string
		max  int
		want []string
	}{
		{"cabe", "olá", 10, []string{"olá"}},
		{"vazio", "  ", 10, nil},
		{"exato", long("a", 10), 10, []string{long("a", 10)}},
		{"parágrafo primeiro", "aaaa bbbb.\n\ncccc dddd. eeee", 14, []string{"aaaa bbbb.", "cccc dddd.", "eeee"}},
		{"depois fim de frase", "um dois tres. quatro cinco seis", 20, []string{"um dois tres.", "quatro cinco seis"}},
		{"depois espaço", "um dois tres quatro", 12, []string{"um dois tres", "quatro"}},
		{"corte duro", long("x", 25), 10, []string{long("x", 10), long("x", 10), long("x", 5)}},
		{"runes, não bytes", long("é", 9), 4, []string{"éééé", "éééé", "é"}},
	}
	for _, c := range cenarios {
		t.Run(c.nome, func(t *testing.T) {
			got := Dividir(c.in, c.max)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Dividir = %q, quer %q", got, c.want)
			}
			for _, b := range got {
				if n := len([]rune(b)); n > c.max {
					t.Errorf("bloco com %d runes > %d", n, c.max)
				}
			}
		})
	}
}
