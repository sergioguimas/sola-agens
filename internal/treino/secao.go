package treino

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxSecao é o teto da seção de estilo: ~500 tokens (T8, S14).
const maxSecao = 2000

const cabecalhoSecao = "ESTILO DO ESTABELECIMENTO (aprendido com a equipe; é jeito de escrever, não informação)"

// SecaoPrompt renderiza o perfil como entra no prompt (specs/06 §9.1). Campo vazio
// some, e perfil vazio devolve "". Passando do teto, cortam-se exemplos do fim e
// depois o vocabulário (S14).
func (p Perfil) SecaoPrompt() string {
	exemplos := len(p.Exemplos)
	for {
		s := p.renderizar(exemplos, true)
		if utf8.RuneCountInString(s) <= maxSecao {
			return s
		}
		if exemplos == 0 {
			return p.renderizar(0, false)
		}
		exemplos--
	}
}

func aspasLista(v []string) string {
	q := make([]string, len(v))
	for i, s := range v {
		q[i] = `"` + s + `"`
	}
	return strings.Join(q, ", ")
}

func (p Perfil) renderizar(exemplos int, vocabulario bool) string {
	var linhas []string
	add := func(partes ...string) {
		var cheias []string
		for _, x := range partes {
			if x != "" {
				cheias = append(cheias, x)
			}
		}
		if len(cheias) > 0 {
			linhas = append(linhas, "- "+strings.Join(cheias, " "))
		}
	}

	tratamento := ""
	if p.Tratamento != nil {
		switch *p.Tratamento {
		case "voce":
			tratamento = `Trate o cliente por "você".`
		case "senhor":
			tratamento = `Trate o cliente por "senhor" ou "senhora".`
		case "pelo_nome":
			tratamento = "Trate o cliente pelo nome."
		}
	}
	tom := ""
	if len(p.Tom) > 0 {
		tom = "Tom: " + strings.Join(p.Tom, ", ") + "."
	}
	add(tratamento, tom)

	saudacoes, despedidas := "", ""
	if len(p.Saudacoes) > 0 {
		saudacoes = "Saudações que a equipe usa: " + aspasLista(p.Saudacoes) + "."
	}
	if len(p.Despedidas) > 0 {
		despedidas = "Despedidas: " + aspasLista(p.Despedidas) + "."
	}
	add(saudacoes, despedidas)

	var emojis []string
	if p.Emojis.Uso != nil {
		switch *p.Emojis.Uso {
		case "nunca":
			emojis = append(emojis, "não use")
		case "pouco":
			emojis = append(emojis, "use pouco")
		case "frequente":
			emojis = append(emojis, "use com frequência")
		}
	}
	if len(p.Emojis.Preferidos) > 0 {
		emojis = append(emojis, "prefira "+strings.Join(p.Emojis.Preferidos, " "))
	}
	if len(p.Emojis.Evitar) > 0 {
		emojis = append(emojis, "nunca use "+strings.Join(p.Emojis.Evitar, " "))
	}
	if len(emojis) > 0 {
		add("Emojis: " + strings.Join(emojis, "; ") + ".")
	}

	var pontuacao []string
	if p.Pontuacao.Exclamacao != nil {
		pontuacao = append(pontuacao, "exclamação "+*p.Pontuacao.Exclamacao)
	}
	if p.Pontuacao.Reticencias != nil {
		pontuacao = append(pontuacao, simOuSem(*p.Pontuacao.Reticencias, "reticências"))
	}
	if p.Pontuacao.Risada != nil {
		pontuacao = append(pontuacao, simOuSem(*p.Pontuacao.Risada, `"kkk"`))
	}
	frase := ""
	if len(pontuacao) > 0 {
		frase = strings.ToUpper(pontuacao[0][:1]) + strings.Join(pontuacao, "; ")[1:] + "."
	}
	comprimento := ""
	if p.Comprimento != nil {
		comprimento = map[string]string{"curto": "Mensagens curtas.", "medio": "Mensagens de tamanho médio."}[*p.Comprimento]
	}
	add(frase, comprimento)

	if vocabulario {
		preferir, evitar := "", ""
		if len(p.Vocabulario.Preferir) > 0 {
			preferir = "Prefira: " + aspasLista(p.Vocabulario.Preferir) + "."
		}
		if len(p.Vocabulario.Evitar) > 0 {
			evitar = "Evite: " + aspasLista(p.Vocabulario.Evitar) + "."
		}
		add(preferir, evitar)
	}

	if exemplos > 0 {
		linhas = append(linhas, "- Exemplos de como a equipe responde (imite o jeito; NUNCA copie preço, horário ou promessa daqui):")
		for _, e := range p.Exemplos[:exemplos] {
			linhas = append(linhas, fmt.Sprintf(`  · %s: "%s"`, e.Cenario, strings.ReplaceAll(e.Resposta, "\n", " ")))
		}
	}
	if len(linhas) == 0 {
		return ""
	}
	return cabecalhoSecao + "\n" + strings.Join(linhas, "\n")
}

func simOuSem(usa bool, item string) string {
	if usa {
		return "com " + item
	}
	return "sem " + item
}
