package agent

import "strings"

// LimiteMensagem é o máximo de runes que o sistema aceita por mensagem
// (enviarMensagem: texto ≤ 4000, specs/04 §2).
const LimiteMensagem = 4000

// Dividir parte o texto em blocos de até max runes, em ordem. Corta no último
// parágrafo ("\n\n") que couber; sem parágrafo, no fim de frase; sem frase, no
// último espaço; sem espaço, no limite. O espaço em branco da emenda é
// descartado; nenhum outro texto se perde.
func Dividir(s string, max int) []string {
	if max <= 0 {
		max = 1
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return nil
	}
	var out []string
	for len(r) > max {
		corte := pontoDeCorte(r[:max+1])
		bloco := strings.TrimRight(string(r[:corte]), " \t\r\n")
		if bloco != "" {
			out = append(out, bloco)
		}
		r = []rune(strings.TrimLeft(string(r[corte:]), " \t\r\n"))
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// pontoDeCorte recebe max+1 runes (o último é só para ver se o corte cai numa
// fronteira) e devolve o índice (exclusivo) onde cortar, entre 1 e max.
func pontoDeCorte(janela []rune) int {
	n := len(janela)
	for i := n - 1; i > 0; i-- { // parágrafo
		if janela[i] == '\n' && janela[i-1] == '\n' {
			return i - 1
		}
	}
	for i := n - 1; i > 0; i-- { // fim de frase: pontuação seguida de espaço/quebra
		if (janela[i] == ' ' || janela[i] == '\n') && (janela[i-1] == '.' || janela[i-1] == '!' || janela[i-1] == '?') {
			return i
		}
	}
	for i := n - 1; i > 0; i-- { // espaço
		if janela[i] == ' ' || janela[i] == '\n' {
			return i
		}
	}
	return n - 1
}
