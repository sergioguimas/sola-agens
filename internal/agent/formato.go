package agent

import "regexp"

// negritoDuplo casa o negrito do Markdown (**texto**) dentro de uma linha.
var negritoDuplo = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)

// FormatarWhatsApp troca o negrito do Markdown (**texto**) pelo do WhatsApp
// (*texto*). O prompt já pede asterisco simples, mas o modelo escorrega e o
// WhatsApp mostra os asteriscos sobrando (1º teste real, 27/10). Asterisco
// solto ou sem par fica como veio.
func FormatarWhatsApp(s string) string {
	return negritoDuplo.ReplaceAllString(s, "*$1*")
}
