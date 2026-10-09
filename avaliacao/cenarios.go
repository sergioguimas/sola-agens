package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

//go:embed cenarios.yaml
var cenariosPadrao []byte

// Cenario é uma conversa de teste do modelo e do prompt (CHECKLIST-ACEITE).
type Cenario struct {
	ID           int         `yaml:"id"`
	Nome         string      `yaml:"nome"`
	Cliente      Cliente     `yaml:"cliente"`
	Agendamentos []AgSemente `yaml:"agendamentos"`
	// NomesCadastrados são nomes que já existem em OUTRO número (verificarNomeCadastrado).
	NomesCadastrados []string `yaml:"nomes_cadastrados"`
	Atendente        *Persona `yaml:"atendente"`
	// ElizaFalha = "ferramentas": toda ferramenta de negócio responde 503.
	ElizaFalha  string       `yaml:"eliza_falha"`
	Rodadas     []Rodada     `yaml:"rodadas"`
	EstadoFinal *EstadoFinal `yaml:"estado_final"`
}

// Cliente é a identificação que o Eliza simulado devolve no contexto.
type Cliente struct {
	Situacao     string `yaml:"situacao"` // identificado | desconhecido | ambiguo
	PrimeiroNome string `yaml:"primeiro_nome"`
	NomeExibicao string `yaml:"nome_exibicao"` // pushName do WhatsApp
}

// AgSemente é um agendamento que já existe no início do cenário.
type AgSemente struct {
	Ref          string `yaml:"ref"`
	Status       string `yaml:"status"` // pending | scheduled | confirmed
	Inicio       string `yaml:"inicio"` // AAAA-MM-DDTHH:mm, relógio local
	Servico      string `yaml:"servico"`
	Profissional string `yaml:"profissional"`
	// Aguardando entra em aguardandoConfirmacao enquanto estiver scheduled.
	Aguardando bool `yaml:"aguardando"`
}

// Persona é a personalização do atendente (campo atendente do contexto).
type Persona struct {
	Persona string `yaml:"persona"`
	Tom     string `yaml:"tom"`
}

// Rodada é um turno: as mensagens do cliente que o debounce juntaria.
type Rodada struct {
	Mensagens []string `yaml:"mensagens"`
	Esperar   Esperar  `yaml:"esperar"`
}

// Esperar são as verificações de uma rodada. Em resposta_contem /
// resposta_nao_contem, "a|b" vale por "a" OU "b"; sem acento e sem caixa.
type Esperar struct {
	FerramentasChamadas  []string `yaml:"ferramentas_chamadas"`
	FerramentasProibidas []string `yaml:"ferramentas_proibidas"`
	EscalouCom           string   `yaml:"escalou_com"`
	NaoEscalou           bool     `yaml:"nao_escalou"`
	Fastpath             *bool    `yaml:"fastpath"`
	Modo                 string   `yaml:"modo"`
	Resultado            string   `yaml:"resultado"`
	RespostaContem       []string `yaml:"resposta_contem"`
	RespostaNaoContem    []string `yaml:"resposta_nao_contem"`
}

// EstadoFinal confere o estado do Eliza simulado no fim do cenário.
type EstadoFinal struct {
	Agendamentos      []CondAg `yaml:"agendamentos"`
	AgendamentosNovos *int     `yaml:"agendamentos_novos"`
	CadastroCriado    *bool    `yaml:"cadastro_criado"`
}

// CondAg: algum agendamento do estado final precisa casar com todos os campos preenchidos.
type CondAg struct {
	Ref           string `yaml:"ref"`
	Status        string `yaml:"status"`
	Servico       string `yaml:"servico"`
	Profissional  string `yaml:"profissional"`
	InicioPrefixo string `yaml:"inicio_prefixo"`
	Novo          *bool  `yaml:"novo"`
}

var (
	situacoes     = map[string]bool{"identificado": true, "desconhecido": true, "ambiguo": true}
	statusSemente = map[string]bool{"pending": true, "scheduled": true, "confirmed": true}
	statusFinal   = map[string]bool{"pending": true, "scheduled": true, "confirmed": true, "canceled": true}
	servicosChave = map[string]bool{"corte": true, "manicure": true}
	profChave     = map[string]bool{"bia": true, "caio": true}
	motivosEscala = map[string]bool{"pedido_do_cliente": true, "cliente_ambiguo": true, "fora_da_politica": true,
		"nao_entendi": true, "reclamacao": true, "numero_novo": true, "falha_tecnica": true, "outro": true}
)

// CarregarCenarios lê o yaml (campo desconhecido é erro) e valida a estrutura.
func CarregarCenarios(raw []byte) ([]Cenario, error) {
	var cs []Cenario
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cs); err != nil {
		return nil, fmt.Errorf("cenarios: yaml inválido: %w", err)
	}
	if err := ValidarCenarios(cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// CarregarArquivo lê os cenários de um arquivo; caminho vazio = os embutidos.
func CarregarArquivo(caminho string) ([]Cenario, error) {
	if caminho == "" {
		return CarregarCenarios(cenariosPadrao)
	}
	raw, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	return CarregarCenarios(raw)
}

// ValidarCenarios junta todos os problemas numa mensagem só.
func ValidarCenarios(cs []Cenario) error {
	if len(cs) == 0 {
		return errors.New("cenarios: lista vazia")
	}
	var erros []string
	ruim := func(c Cenario, f string, a ...any) {
		erros = append(erros, fmt.Sprintf("cenário %d: %s", c.ID, fmt.Sprintf(f, a...)))
	}
	vistos := map[int]bool{}
	for _, c := range cs {
		if c.ID <= 0 || vistos[c.ID] {
			ruim(c, "id ausente ou repetido")
		}
		vistos[c.ID] = true
		if strings.TrimSpace(c.Nome) == "" {
			ruim(c, "nome vazio")
		}
		if !situacoes[c.Cliente.Situacao] {
			ruim(c, "cliente.situacao inválida (%q)", c.Cliente.Situacao)
		}
		if c.Cliente.Situacao == "identificado" && c.Cliente.PrimeiroNome == "" {
			ruim(c, "identificado exige cliente.primeiro_nome")
		}
		if c.Cliente.Situacao != "identificado" && len(c.Agendamentos) > 0 {
			ruim(c, "só cliente identificado tem agendamentos")
		}
		refs := map[string]bool{}
		for _, a := range c.Agendamentos {
			if a.Ref == "" || refs[a.Ref] {
				ruim(c, "agendamento com ref ausente ou repetida (%q)", a.Ref)
			}
			refs[a.Ref] = true
			if !statusSemente[a.Status] {
				ruim(c, "agendamento %s: status inválido (%q)", a.Ref, a.Status)
			}
			if _, err := time.Parse("2006-01-02T15:04", a.Inicio); err != nil {
				ruim(c, "agendamento %s: inicio inválido (%q)", a.Ref, a.Inicio)
			}
			if !servicosChave[a.Servico] || !profChave[a.Profissional] {
				ruim(c, "agendamento %s: servico/profissional desconhecido", a.Ref)
			}
			if a.Aguardando && a.Status != "scheduled" {
				ruim(c, "agendamento %s: aguardando exige status scheduled", a.Ref)
			}
		}
		if c.ElizaFalha != "" && c.ElizaFalha != "ferramentas" {
			ruim(c, "eliza_falha desconhecida (%q)", c.ElizaFalha)
		}
		if len(c.Rodadas) == 0 {
			ruim(c, "sem rodadas")
		}
		for i, r := range c.Rodadas {
			n := i + 1
			if len(r.Mensagens) == 0 {
				ruim(c, "rodada %d sem mensagens", n)
			}
			for _, m := range r.Mensagens {
				if strings.TrimSpace(m) == "" {
					ruim(c, "rodada %d com mensagem vazia", n)
				}
			}
			e := r.Esperar
			if e.EscalouCom != "" && !motivosEscala[e.EscalouCom] {
				ruim(c, "rodada %d: escalou_com desconhecido (%q)", n, e.EscalouCom)
			}
			if e.EscalouCom != "" && e.NaoEscalou {
				ruim(c, "rodada %d: escalou_com e nao_escalou juntos", n)
			}
			for _, f := range e.FerramentasChamadas {
				for _, p := range e.FerramentasProibidas {
					if f == p {
						ruim(c, "rodada %d: %s é chamada e proibida", n, f)
					}
				}
			}
			for _, s := range append(append([]string{}, e.RespostaContem...), e.RespostaNaoContem...) {
				if strings.TrimSpace(s) == "" {
					ruim(c, "rodada %d: texto de resposta vazio", n)
				}
			}
		}
		if ef := c.EstadoFinal; ef != nil {
			for _, k := range ef.Agendamentos {
				if k.Status != "" && !statusFinal[k.Status] {
					ruim(c, "estado_final: status inválido (%q)", k.Status)
				}
				if k.Ref != "" && !refs[k.Ref] {
					ruim(c, "estado_final: ref %q não existe", k.Ref)
				}
				if (k.Servico != "" && !servicosChave[k.Servico]) || (k.Profissional != "" && !profChave[k.Profissional]) {
					ruim(c, "estado_final: servico/profissional desconhecido")
				}
			}
		}
	}
	if len(erros) > 0 {
		return errors.New(strings.Join(erros, "\n"))
	}
	return nil
}

// dobrar tira acento e caixa para comparar texto.
func dobrar(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// contemAlgum: "a|b" casa se qualquer alternativa aparece no texto.
func contemAlgum(texto, padrao string) bool {
	t := dobrar(texto)
	for _, alt := range strings.Split(padrao, "|") {
		if a := strings.TrimSpace(dobrar(alt)); a != "" && strings.Contains(t, a) {
			return true
		}
	}
	return false
}
