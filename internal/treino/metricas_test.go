package treino

import (
	"reflect"
	"strings"
	"testing"

	"sola-agens/internal/store"
)

func TestAcharEmojis(t *testing.T) {
	casos := []struct {
		nome, texto string
		quer        []string
	}{
		{"sem emoji", "oi, tudo bem?", nil},
		{"simples", "oi 💅 ✨", []string{"💅", "✨"}},
		{"tom de pele conta como um", "valeu 💅🏻", []string{"💅🏻"}},
		{"ZWJ conta como um", "👩🏽‍💻 aqui", []string{"👩🏽‍💻"}},
		{"seletor de variação some", "❤️ e ❤", []string{"❤", "❤"}},
		{"bandeira é um par", "🇧🇷 viva", []string{"🇧🇷"}},
		{"repetido conta duas vezes", "💅💅", []string{"💅", "💅"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := acharEmojis(c.texto); !reflect.DeepEqual(got, c.quer) {
				t.Errorf("%q = %q, quer %q", c.texto, got, c.quer)
			}
		})
	}
}

func valorDe(obs []store.ObservacaoNova, campo string) string {
	for _, o := range obs {
		if o.Campo == campo {
			return o.Valor
		}
	}
	return ""
}

func TestMedir(t *testing.T) {
	longa := strings.Repeat("x", 121)
	casos := []struct {
		nome, campo, quer string
		respostas         []string
	}{
		{"sem emoji", "emojis.uso", "nunca", []string{"a", "b", "c"}},
		{"pouco emoji", "emojis.uso", "pouco", []string{"a 💅", "b", "c"}},
		{"meio por resposta é frequente", "emojis.uso", "frequente", []string{"a 💅", "b ✨", "c", "d"}},
		{"exclamação rara", "pontuacao.exclamacao", "rara", []string{"a", "b", "c"}},
		{"exclamação normal", "pontuacao.exclamacao", "normal", []string{"a!", "b", "c"}},
		{"exclamação muita", "pontuacao.exclamacao", "muita", []string{"a!", "b!", "c!"}},
		{"reticências com ...", "pontuacao.reticencias", "sim", []string{"a...", "b", "c"}},
		{"reticências com …", "pontuacao.reticencias", "sim", []string{"a…", "b", "c"}},
		{"reticências abaixo de 30%", "pontuacao.reticencias", "nao", []string{"a...", "b", "c", "d"}},
		{"risada kkk", "pontuacao.risada", "sim", []string{"kkkk boa", "b", "c"}},
		{"risada rs", "pontuacao.risada", "sim", []string{"boa rs", "b", "c"}},
		{"haha", "pontuacao.risada", "sim", []string{"hahaha", "b", "c"}},
		{"rs dentro de palavra não é risada", "pontuacao.risada", "nao", []string{"cursos", "versão", "c"}},
		{"curto", "comprimento", "curto", []string{"a", "b", "c"}},
		{"mediana acima de 120", "comprimento", "medio", []string{longa, longa, "c"}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := valorDe(medir(c.respostas), c.campo); got != c.quer {
				t.Errorf("%s = %q, quer %q", c.campo, got, c.quer)
			}
		})
	}
}

func TestMedirPreferidosExigemDuasOcorrencias(t *testing.T) {
	obs := medir([]string{"a 💅", "b 💅 ✨", "c"})
	var preferidos []string
	for _, o := range obs {
		if o.Campo == "emojis.preferidos" {
			preferidos = append(preferidos, o.Valor)
			if o.Ocorrencias != 2 || o.Origem != store.OrigemMetrica {
				t.Errorf("observação: %+v", o)
			}
		}
	}
	if !reflect.DeepEqual(preferidos, []string{"💅"}) {
		t.Errorf("preferidos: %q", preferidos)
	}
}

func TestPoucaAmostraNaoGeraMetrica(t *testing.T) {
	if obs := medir([]string{"a!", "b!"}); obs != nil {
		t.Errorf("com 2 respostas: %+v", obs)
	}
}
