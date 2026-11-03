package treino

import (
	"slices"
	"strings"
	"unicode/utf8"

	"sola-agens/internal/llm"
	"sola-agens/internal/store"
)

const (
	nomeIniciarEnsaio        = "iniciar_ensaio"
	nomeRegistrarPreferencia = "registrar_preferencia"

	maxFalaCliente = 300
	maxValor       = 80
	maxCitacao     = 200
)

func enumDe(valores []string) []any {
	out := make([]any, len(valores))
	for i, v := range valores {
		out[i] = v
	}
	return out
}

// decls são as ferramentas do modo treino: do motor, nunca da OpenAPI (specs/06 §6.3).
func decls() []llm.ToolDecl {
	return []llm.ToolDecl{
		{
			Name:        nomeIniciarEnsaio,
			Description: "Começa um ensaio: você fala como um cliente e espera a resposta da pessoa da equipe. Depois de chamar, escreva só a fala do cliente, entre aspas.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cenario":      map[string]any{"type": "string", "enum": enumDe(Cenarios), "description": "Situação do ensaio."},
					"fala_cliente": map[string]any{"type": "string", "description": "O que o cliente escreve, até 300 caracteres."},
				},
				"required": []any{"cenario", "fala_cliente"},
			},
		},
		{
			Name:        nomeRegistrarPreferencia,
			Description: "Guarda uma preferência que a pessoa da equipe DECLAROU sobre o próprio jeito de escrever, como \"nunca uso 😂\". Não use para o que só apareceu numa resposta de ensaio: esses padrões o sistema extrai sozinho.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"campo":   map[string]any{"type": "string", "enum": enumDe(camposPreferencia), "description": "Campo do perfil de estilo."},
					"valor":   map[string]any{"type": "string", "description": "A preferência, até 80 caracteres."},
					"citacao": map[string]any{"type": "string", "description": "Trecho literal do que a pessoa escreveu, até 200 caracteres."},
				},
				"required": []any{"campo", "valor", "citacao"},
			},
		},
	}
}

// Ensaio é a situação em que o agens fala como cliente.
type Ensaio struct {
	Cenario string `json:"cenario"`
	Fala    string `json:"fala"`
}

// Preferencia é o que o operador declarou. Fica no roteiro da sessão até a
// fusão do encerramento (specs/06 §6.3).
type Preferencia struct {
	Campo   string `json:"campo"`
	Valor   string `json:"valor"`
	Citacao string `json:"citacao"`
}

func texto(args map[string]any, nome string) string {
	v, _ := args[nome].(string)
	return strings.TrimSpace(v)
}

func recusa(motivo string) map[string]any { return map[string]any{"ok": false, "motivo": motivo} }

func validarEnsaio(args map[string]any) (Ensaio, map[string]any) {
	e := Ensaio{Cenario: texto(args, "cenario"), Fala: texto(args, "fala_cliente")}
	if !slices.Contains(Cenarios, e.Cenario) {
		return e, recusa("cenario inválido")
	}
	if e.Fala == "" || utf8.RuneCountInString(e.Fala) > maxFalaCliente {
		return e, recusa("fala_cliente vazia ou maior que 300 caracteres")
	}
	return e, nil
}

// validarPreferencia exige que a citação seja mesmo do operador: a preferência
// declarada entra no perfil já com uma sessão (specs/06 §7.3), então não pode
// nascer de frase inventada pelo modelo (S17).
func validarPreferencia(args map[string]any, doOperador []string) (Preferencia, map[string]any) {
	p := Preferencia{Campo: texto(args, "campo"), Valor: texto(args, "valor"), Citacao: texto(args, "citacao")}
	switch {
	case !slices.Contains(camposPreferencia, p.Campo):
		return p, recusa("campo inválido")
	case p.Valor == "" || utf8.RuneCountInString(p.Valor) > maxValor:
		return p, recusa("valor vazio ou maior que 80 caracteres")
	case p.Citacao == "" || utf8.RuneCountInString(p.Citacao) > maxCitacao:
		return p, recusa("citacao vazia ou maior que 200 caracteres")
	}
	cit := normalizar(p.Citacao)
	for _, t := range doOperador {
		if strings.Contains(normalizar(t), cit) {
			return p, nil
		}
	}
	return p, recusa("citacao precisa ser trecho literal do que a pessoa escreveu")
}

func normalizar(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// falasDoOperador junta o que o operador escreveu na sessão, com o texto do turno.
func falasDoOperador(msgs []store.MensagemTreino, atual string) []string {
	var out []string
	for _, m := range msgs {
		if m.Papel == store.PapelOperador && m.Conteudo != nil {
			out = append(out, *m.Conteudo)
		}
	}
	if atual != "" {
		out = append(out, atual)
	}
	return out
}
