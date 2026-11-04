package treino

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"sola-agens/internal/store"
)

// Limites do perfil (specs/06 §8).
const (
	maxSaudacoes     = 5
	maxTextoCurto    = 80
	maxEmojis        = 8
	maxVocabulario   = 15
	maxPalavra       = 40
	maxTom           = 5
	maxDescritorTom  = 30
	maxExemplos      = 8
	maxExemplo       = 300
	maxNota          = 300
	sessoesEvidencia = 2 // T5
)

// Perfil é o estilo em vigor de um tenant (specs/06 §8). Os nomes de campo são
// os do Zod do Eliza (contrato 10 §4).
type Perfil struct {
	Tratamento  *string     `json:"tratamento"`
	Saudacoes   []string    `json:"saudacoes"`
	Despedidas  []string    `json:"despedidas"`
	Emojis      Emojis      `json:"emojis"`
	Pontuacao   Pontuacao   `json:"pontuacao"`
	Comprimento *string     `json:"comprimento"`
	Vocabulario Vocabulario `json:"vocabulario"`
	Tom         []string    `json:"tom"`
	Exemplos    []Exemplo   `json:"exemplos"`
}

// Emojis do perfil.
type Emojis struct {
	Uso        *string  `json:"uso"`
	Preferidos []string `json:"preferidos"`
	Evitar     []string `json:"evitar"`
}

// Pontuacao do perfil.
type Pontuacao struct {
	Exclamacao  *string `json:"exclamacao"`
	Reticencias *bool   `json:"reticencias"`
	Risada      *bool   `json:"risada"`
}

// Vocabulario do perfil.
type Vocabulario struct {
	Preferir []string `json:"preferir"`
	Evitar   []string `json:"evitar"`
}

// Exemplo é uma resposta real da equipe a um cenário.
type Exemplo struct {
	Cenario  string `json:"cenario"`
	Resposta string `json:"resposta"`
}

// Mudanca é a diferença de um campo entre duas versões.
type Mudanca struct {
	Campo  string `json:"campo"`
	Antes  any    `json:"antes"`
	Depois any    `json:"depois"`
}

// novoPerfil devolve o perfil vazio, com listas [] e não null: o Zod do Eliza
// exige array.
func novoPerfil() Perfil {
	return Perfil{
		Saudacoes: []string{}, Despedidas: []string{}, Tom: []string{}, Exemplos: []Exemplo{},
		Emojis:      Emojis{Preferidos: []string{}, Evitar: []string{}},
		Vocabulario: Vocabulario{Preferir: []string{}, Evitar: []string{}},
	}
}

func lerPerfil(raw []byte) (Perfil, error) {
	p := novoPerfil()
	if len(raw) == 0 {
		return p, nil
	}
	return p, json.Unmarshal(raw, &p)
}

// campos lista o perfil por nome, na ordem do esquema, para comparar versões.
func (p Perfil) campos() []Mudanca {
	txt := func(s *string) any {
		if s == nil {
			return nil
		}
		return *s
	}
	boolean := func(b *bool) any {
		if b == nil {
			return nil
		}
		return *b
	}
	return []Mudanca{
		{Campo: "tratamento", Depois: txt(p.Tratamento)},
		{Campo: "saudacoes", Depois: p.Saudacoes},
		{Campo: "despedidas", Depois: p.Despedidas},
		{Campo: "emojis.uso", Depois: txt(p.Emojis.Uso)},
		{Campo: "emojis.preferidos", Depois: p.Emojis.Preferidos},
		{Campo: "emojis.evitar", Depois: p.Emojis.Evitar},
		{Campo: "pontuacao.exclamacao", Depois: txt(p.Pontuacao.Exclamacao)},
		{Campo: "pontuacao.reticencias", Depois: boolean(p.Pontuacao.Reticencias)},
		{Campo: "pontuacao.risada", Depois: boolean(p.Pontuacao.Risada)},
		{Campo: "comprimento", Depois: txt(p.Comprimento)},
		{Campo: "vocabulario.preferir", Depois: p.Vocabulario.Preferir},
		{Campo: "vocabulario.evitar", Depois: p.Vocabulario.Evitar},
		{Campo: "tom", Depois: p.Tom},
		{Campo: "exemplos", Depois: p.Exemplos},
	}
}

// diferencas lista os campos que mudaram de a para b.
func diferencas(a, b Perfil) []Mudanca {
	antes, depois := a.campos(), b.campos()
	out := []Mudanca{}
	for i := range depois {
		if !reflect.DeepEqual(antes[i].Depois, depois[i].Depois) {
			out = append(out, Mudanca{Campo: depois[i].Campo, Antes: antes[i].Depois, Depois: depois[i].Depois})
		}
	}
	return out
}

// elegivel diz se a observação já entra no perfil (specs/06 §7.3). Exemplo entra
// com uma sessão: é a resposta real, não uma inferência (S16).
func elegivel(o store.Observacao) bool {
	if o.Estado == store.ObservacaoRejeitada {
		return false
	}
	return o.Campo == "exemplos" || o.Sessoes >= sessoesEvidencia || o.Origem == store.OrigemDeclarada
}

// maisForte ordena por sessões e, no empate, pela sessão mais recente (uuid v7
// ordena pelo tempo).
func maisForte(a, b store.Observacao) int {
	if a.Sessoes != b.Sessoes {
		return b.Sessoes - a.Sessoes
	}
	return strings.Compare(b.UltimaSessao, a.UltimaSessao)
}

// derivar monta o perfil a partir das observações elegíveis.
func derivar(obs []store.Observacao) Perfil {
	porCampo := map[string][]store.Observacao{}
	for _, o := range obs {
		if elegivel(o) {
			porCampo[o.Campo] = append(porCampo[o.Campo], o)
		}
	}
	for _, l := range porCampo {
		slices.SortStableFunc(l, maisForte)
	}
	unico := func(campo string) *string {
		if l := porCampo[campo]; len(l) > 0 {
			v := l[0].Valor
			return &v
		}
		return nil
	}
	sn := func(campo string) *bool {
		v := unico(campo)
		if v == nil {
			return nil
		}
		b := *v == "sim"
		return &b
	}
	lista := func(campo string, limite int, tirar []string) []string {
		out := []string{}
		for _, o := range porCampo[campo] {
			if len(out) == limite {
				break
			}
			if !slices.Contains(tirar, o.ValorExibicao) {
				out = append(out, o.ValorExibicao)
			}
		}
		return out
	}

	p := novoPerfil()
	p.Tratamento = unico("tratamento")
	p.Saudacoes = lista("saudacoes", maxSaudacoes, nil)
	p.Despedidas = lista("despedidas", maxSaudacoes, nil)
	p.Emojis.Uso = unico("emojis.uso")
	p.Emojis.Evitar = lista("emojis.evitar", maxEmojis, nil)
	// O que a pessoa pediu para evitar vence o que ela usou.
	p.Emojis.Preferidos = lista("emojis.preferidos", maxEmojis, p.Emojis.Evitar)
	p.Pontuacao.Exclamacao = unico("pontuacao.exclamacao")
	p.Pontuacao.Reticencias = sn("pontuacao.reticencias")
	p.Pontuacao.Risada = sn("pontuacao.risada")
	p.Comprimento = unico("comprimento")
	p.Vocabulario.Evitar = lista("vocabulario.evitar", maxVocabulario, nil)
	p.Vocabulario.Preferir = lista("vocabulario.preferir", maxVocabulario, p.Vocabulario.Evitar)
	p.Tom = lista("tom", maxTom, nil)

	// Um exemplo por cenário: o mais recente, na ordem da lista de cenários.
	mais := map[string]store.Observacao{}
	for _, o := range porCampo["exemplos"] {
		if atual, ok := mais[o.Cenario]; !ok || o.UltimaSessao > atual.UltimaSessao {
			mais[o.Cenario] = o
		}
	}
	for _, c := range Cenarios {
		if o, ok := mais[c]; ok && len(p.Exemplos) < maxExemplos {
			p.Exemplos = append(p.Exemplos, Exemplo{Cenario: c, Resposta: o.ValorExibicao})
		}
	}
	return p
}

// valoresDoCampo limita os valores fechados do perfil (specs/06 §8).
var valoresDoCampo = map[string][]string{
	"emojis.uso":           {"nunca", "pouco", "frequente"},
	"pontuacao.exclamacao": {"rara", "normal", "muita"},
	"comprimento":          {"curto", "medio"},
}

// canonizar leva o valor ao que o perfil aceita e devolve os pares (normalizado,
// exibição). Vazio = não aproveitável. Uma declaração em texto livre pode trazer
// vários emojis, e cada um vira uma observação.
func canonizar(campo, valor string) [][2]string {
	limpo := limpar(valor, limiteDoCampo(campo), campo == "exemplos")
	if limpo == "" {
		return nil
	}
	chave := semAcento(normalizar(limpo))
	um := func(v string) [][2]string { return [][2]string{{v, v}} }
	switch campo {
	case "tratamento":
		switch {
		case strings.Contains(chave, "senhor"):
			return um("senhor")
		case strings.Contains(chave, "nome"):
			return um("pelo_nome")
		case strings.Contains(chave, "voce"):
			return um("voce")
		}
		return nil
	case "emojis.uso", "pontuacao.exclamacao", "comprimento":
		chave = strings.TrimSuffix(chave, "s")
		for _, v := range valoresDoCampo[campo] {
			if chave == v || strings.Contains(chave, v) {
				return um(v)
			}
		}
		return nil
	case "pontuacao.reticencias", "pontuacao.risada":
		switch {
		case strings.Contains(chave, "nao"), strings.Contains(chave, "nunca"), strings.Contains(chave, "evit"), chave == "false":
			return um("nao")
		case strings.Contains(chave, "sim"), strings.Contains(chave, "usa"), strings.Contains(chave, "uso"), chave == "true":
			return um("sim")
		}
		return nil
	case "emojis.preferidos", "emojis.evitar":
		var out [][2]string
		for _, e := range acharEmojis(limpo) {
			out = append(out, [2]string{e, e})
		}
		return out
	}
	// Emoji solto tem campo próprio; aqui só entra o que tem palavra.
	if strings.HasPrefix(campo, "vocabulario.") && strings.IndexFunc(limpo, unicode.IsLetter) < 0 {
		return nil
	}
	return [][2]string{{normalizar(limpo), limpo}}
}

func limiteDoCampo(campo string) int {
	switch campo {
	case "saudacoes", "despedidas":
		return maxTextoCurto
	case "vocabulario.preferir", "vocabulario.evitar":
		return maxPalavra
	case "tom":
		return maxDescritorTom
	case "exemplos":
		return maxExemplo
	}
	return maxValor
}

// limpar tira caracteres de controle, troca quebra de linha por espaço (em
// exemplos mantém até 2) e corta no limite (specs/06 §8). O perfil vai ao prompt
// de sistema: é a defesa contra quebra de seção vinda do treino.
func limpar(s string, limite int, manterQuebras bool) string {
	quebras := 2
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == '\n':
			if manterQuebras && quebras > 0 {
				quebras--
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		case r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if utf8.RuneCountInString(out) > limite {
		out = string([]rune(out)[:limite])
	}
	return strings.TrimSpace(out)
}

var acentos = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o", "õ", "o", "ú", "u", "ç", "c")

func semAcento(s string) string { return acentos.Replace(s) }
