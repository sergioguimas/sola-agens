package treino

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"sola-agens/internal/store"
)

// minRespostas: com menos que isso a sessão não produz métricas (specs/06 §7.1).
const minRespostas = 3

var risada = regexp.MustCompile(`(?i)\b(k{3,}|(ha){2,}|(rs)+)\b`)

// medir conta o estilo nas respostas de ensaio, sem LLM: determinístico,
// testável e de graça (S11).
func medir(respostas []string) []store.ObservacaoNova {
	if len(respostas) < minRespostas {
		return nil
	}
	n := float64(len(respostas))
	var out []store.ObservacaoNova
	unico := func(campo, valor string) {
		out = append(out, store.ObservacaoNova{Campo: campo, Valor: valor, ValorExibicao: valor,
			Origem: store.OrigemMetrica, Ocorrencias: 1})
	}

	contagem := map[string]int{}
	var ordem []string
	totalEmojis, exclamacoes, comReticencias, comRisada := 0, 0, 0, 0
	tamanhos := make([]int, 0, len(respostas))
	for _, r := range respostas {
		for _, e := range acharEmojis(r) {
			if contagem[e] == 0 {
				ordem = append(ordem, e)
			}
			contagem[e]++
			totalEmojis++
		}
		exclamacoes += strings.Count(r, "!")
		if strings.Contains(r, "...") || strings.Contains(r, "…") {
			comReticencias++
		}
		if risada.MatchString(r) {
			comRisada++
		}
		tamanhos = append(tamanhos, utf8.RuneCountInString(r))
	}

	switch media := float64(totalEmojis) / n; {
	case totalEmojis == 0:
		unico("emojis.uso", "nunca")
	case media < 0.5:
		unico("emojis.uso", "pouco")
	default:
		unico("emojis.uso", "frequente")
	}
	for _, e := range ordem {
		if contagem[e] >= 2 {
			out = append(out, store.ObservacaoNova{Campo: "emojis.preferidos", Valor: e, ValorExibicao: e,
				Origem: store.OrigemMetrica, Ocorrencias: contagem[e]})
		}
	}
	switch media := float64(exclamacoes) / n; {
	case media < 0.2:
		unico("pontuacao.exclamacao", "rara")
	case media < 1:
		unico("pontuacao.exclamacao", "normal")
	default:
		unico("pontuacao.exclamacao", "muita")
	}
	unico("pontuacao.reticencias", simNao(float64(comReticencias)/n >= 0.3))
	unico("pontuacao.risada", simNao(comRisada >= 1))
	if mediana(tamanhos) <= 120 {
		unico("comprimento", "curto")
	} else {
		unico("comprimento", "medio")
	}
	return out
}

func simNao(v bool) string {
	if v {
		return "sim"
	}
	return "nao"
}

func mediana(v []int) float64 {
	s := slices.Sorted(slices.Values(v))
	m := len(s) / 2
	if len(s)%2 == 1 {
		return float64(s[m])
	}
	return float64(s[m-1]+s[m]) / 2
}

const (
	zwj        = 0x200D
	seletorVS  = 0xFE0F
	tomInicial = 0x1F3FB
	tomFinal   = 0x1F3FF
)

func ehBandeira(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

func ehEmoji(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x1F000 && r <= 0x1F2FF:
		return !ehBandeira(r)
	case r == 0x2B50, r == 0x2B55:
		return true
	}
	return false
}

// acharEmojis separa os emojis do texto. Modificador de tom de pele, seletor de
// variação e ZWJ fazem parte do mesmo emoji, e o par de letras regionais é uma
// bandeira (specs/06 §14). O seletor de variação sai: ❤ e ❤️ são o mesmo.
func acharEmojis(s string) []string {
	r := []rune(s)
	var out []string
	for i := 0; i < len(r); {
		if ehBandeira(r[i]) && i+1 < len(r) && ehBandeira(r[i+1]) {
			out = append(out, string(r[i:i+2]))
			i += 2
			continue
		}
		if !ehEmoji(r[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(r) {
			switch {
			case r[j] == seletorVS, r[j] >= tomInicial && r[j] <= tomFinal:
				j++
				continue
			case r[j] == zwj && j+1 < len(r) && ehEmoji(r[j+1]):
				j += 2
				continue
			}
			break
		}
		out = append(out, strings.ReplaceAll(string(r[i:j]), string(rune(seletorVS)), ""))
		i = j
	}
	return out
}
