// Package agent executa um turno de atendimento: dicionário de respostas,
// prompt, fastpath, loop do LLM e intervenção humana (specs/02, specs/03).
package agent

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"sola-agens/internal/sistema"
)

// Respostas são os textos fixos do motor (specs/02 §5). {primeiroNome} é
// substituído por Aplicar.
type Respostas struct {
	Confirmado        string
	Cancelado         string
	FalhaTecnica      string
	MidiaNaoSuportada string
	AudioLongo        string
	NaoSuportado      string
	EquipeSemContato  string
}

// Palavras do fastpath (comparadas com o texto inteiro normalizado).
type Palavras struct {
	Confirmar []string
	Cancelar  []string
}

// Dicionario é a configuração do atendente já mesclada: padrão do motor com o
// que o sistema entregou por cima, campo a campo (specs/01 §3).
type Dicionario struct {
	Persona          string // vazio = o agente não se apresenta por nome
	Tom              string
	PausaHumanaHoras int // 0 = quem chama aplica AGENS_PAUSA_HUMANA_HORAS
	Respostas        Respostas
	Palavras         Palavras
}

// Padrao devolve o dicionário do motor (specs/02 §5, specs/01 §3).
func Padrao() Dicionario {
	return Dicionario{
		Tom: "cordial, direto, frases curtas",
		Respostas: Respostas{
			Confirmado:        "Prontinho, {primeiroNome}! Seu horário está confirmado.",
			Cancelado:         "Tudo certo, cancelei seu horário. Se quiser marcar outro, é só me chamar.",
			FalhaTecnica:      "Não consegui resolver agora. Já avisei a equipe, que vai te responder por aqui.",
			MidiaNaoSuportada: "Por enquanto eu só entendo texto e áudio. Pode me escrever?",
			AudioLongo:        "Esse áudio ficou longo pra mim. Pode mandar um mais curto ou escrever?",
			NaoSuportado:      "Por enquanto eu só entendo texto e áudio. Pode me escrever?",
			EquipeSemContato:  "Vou deixar anotado para a equipe, que te responde assim que puder.",
		},
		Palavras: Palavras{
			Confirmar: []string{"sim", "confirmar", "confirmo", "ok", "pode ser", "confirmado", "ta bom", "estarei", "vou sim", "claro", "👍"},
			Cancelar:  []string{"cancelar", "cancela", "desmarcar", "desmarca", "nao vou"},
		},
	}
}

// Mesclar aplica o que o sistema entregou sobre o padrão: nil ou vazio (só
// espaços) do sistema = padrão.
func Mesclar(padrao Dicionario, a sistema.Atendente) Dicionario {
	out := padrao
	txt := func(dst *string, v *string) {
		if v != nil && strings.TrimSpace(*v) != "" {
			*dst = strings.TrimSpace(*v)
		}
	}
	txt(&out.Persona, a.Persona)
	txt(&out.Tom, a.Tom)
	if a.PausaHumanaHoras != nil && *a.PausaHumanaHoras > 0 {
		out.PausaHumanaHoras = *a.PausaHumanaHoras
	}
	r := &out.Respostas
	txt(&r.Confirmado, a.Respostas.Confirmado)
	txt(&r.Cancelado, a.Respostas.Cancelado)
	txt(&r.FalhaTecnica, a.Respostas.FalhaTecnica)
	txt(&r.MidiaNaoSuportada, a.Respostas.MidiaNaoSuportada)
	txt(&r.AudioLongo, a.Respostas.AudioLongo)
	txt(&r.EquipeSemContato, a.Respostas.EquipeSemContato)
	// DECISÃO: "naoSuportado" não existe em sistema.Atendente.Respostas (o 07 do
	// Eliza não o define); por 02 §5 ele é "igual a midiaNaoSuportada", então
	// acompanha a versão já mesclada dessa chave.
	r.NaoSuportado = r.MidiaNaoSuportada
	if l := palavrasValidas(a.Palavras.Confirmar); len(l) > 0 {
		out.Palavras.Confirmar = l
	}
	if l := palavrasValidas(a.Palavras.Cancelar); len(l) > 0 {
		out.Palavras.Cancelar = l
	}
	return out
}

func palavrasValidas(l []string) []string {
	var out []string
	for _, p := range l {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// Normalizar prepara o texto para comparação (specs/02 §5): minúsculas, sem
// acento, espaços colapsados, pontuação das pontas removida. Emoji (símbolo,
// não pontuação) é preservado.
func Normalizar(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r): // acento
			continue
		// DECISÃO: tom de pele (U+1F3FB..1F3FF) e seletor de variação (U+FE0F)
		// saem, para "👍🏽" valer o mesmo que "👍".
		case r >= 0x1F3FB && r <= 0x1F3FF, r == 0xFE0F:
			continue
		}
		b.WriteRune(r)
	}
	t := strings.Join(strings.Fields(b.String()), " ")
	return strings.TrimFunc(t, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
}

// contemPalavra diz se o texto normalizado é igual a alguma palavra (também
// normalizada).
func contemPalavra(lista []string, normalizado string) bool {
	if normalizado == "" {
		return false
	}
	for _, p := range lista {
		if Normalizar(p) == normalizado {
			return true
		}
	}
	return false
}

var (
	reNomeComVirgula = regexp.MustCompile(`[ \t]*,?[ \t]*\{primeiroNome\}`)
	reEspacos        = regexp.MustCompile(`[ \t]{2,}`)
	reEspacoAntes    = regexp.MustCompile(`[ \t]+([,.!?;:])`)
)

// Aplicar substitui {primeiroNome}. DECISÃO (regra exata para nome ausente):
// remove o placeholder junto com os espaços e a vírgula que vêm imediatamente
// antes dele; no que sobrar, tira vírgula/espaço do início (e põe a 1ª letra em
// maiúscula se houve remoção ali), colapsa espaços repetidos e tira espaço
// antes de pontuação. "Prontinho, {primeiroNome}!" → "Prontinho!";
// "{primeiroNome}, seu horário" → "Seu horário"; "Oi {primeiroNome}!" → "Oi!".
func Aplicar(texto, primeiroNome string) string {
	nome := strings.TrimSpace(primeiroNome)
	if nome != "" {
		return strings.ReplaceAll(texto, "{primeiroNome}", nome)
	}
	if !strings.Contains(texto, "{primeiroNome}") {
		return texto
	}
	t := reNomeComVirgula.ReplaceAllString(texto, "")
	t = reEspacos.ReplaceAllString(t, " ")
	t = reEspacoAntes.ReplaceAllString(t, "$1")
	if semInicio := strings.TrimLeft(t, " \t,;:"); semInicio != t {
		t = semInicio
		if r, n := utf8.DecodeRuneInString(t); n > 0 {
			t = string(unicode.ToUpper(r)) + t[n:]
		}
	}
	return t
}
