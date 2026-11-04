package treino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/store"
)

// ErrEstadoInvalido: filtro ou estado fora do que a rota aceita.
var ErrEstadoInvalido = errors.New("estado inválido")

// PerfilAtivo é a resposta de GET /perfil. Sem perfil: versão 0, campos vazios.
type PerfilAtivo struct {
	Versao      int    `json:"versao"`
	Perfil      Perfil `json:"perfil"`
	SecaoPrompt string `json:"secaoPrompt"`
}

// LerPerfil devolve a versão ativa e como ela entraria no prompt.
func (s *Servico) LerPerfil(ctx context.Context, ag agentes.Agente, op Operador) (PerfilAtivo, error) {
	raw, v, err := s.Store.PerfilAtivo(ctx, ag.ID, op.Tenant)
	if err != nil {
		return PerfilAtivo{}, err
	}
	p, err := lerPerfil(raw)
	if err != nil {
		return PerfilAtivo{}, err
	}
	return PerfilAtivo{Versao: v, Perfil: p, SecaoPrompt: p.SecaoPrompt()}, nil
}

// VersaoResumo é uma linha de GET /perfil/versoes.
type VersaoResumo struct {
	Versao   int       `json:"versao"`
	Ativa    bool      `json:"ativa"`
	Origem   string    `json:"origem"`
	CriadaEm string    `json:"criadaEm"`
	Mudancas []Mudanca `json:"mudancas"`
}

// ListaVersoes é a resposta de GET /perfil/versoes.
type ListaVersoes struct {
	Versoes []VersaoResumo `json:"versoes"`
}

// Versoes lista as versões, a mais nova primeiro.
func (s *Servico) Versoes(ctx context.Context, ag agentes.Agente, op Operador) (ListaVersoes, error) {
	vs, err := s.Store.Versoes(ctx, ag.ID, op.Tenant)
	if err != nil {
		return ListaVersoes{}, err
	}
	out := ListaVersoes{Versoes: make([]VersaoResumo, 0, len(vs))}
	for _, v := range vs {
		m := []Mudanca{}
		if err := json.Unmarshal(v.DiffJSON, &m); err != nil {
			return ListaVersoes{}, fmt.Errorf("diff da versão %d: %w", v.Versao, err)
		}
		out.Versoes = append(out.Versoes, VersaoResumo{Versao: v.Versao, Ativa: v.Ativa, Origem: v.Origem,
			CriadaEm: iso(v.CriadaEm), Mudancas: m})
	}
	return out, nil
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Rejeitada é um padrão que saiu junto com a versão desfeita.
type Rejeitada struct {
	ID    string `json:"id"`
	Campo string `json:"campo"`
	Valor string `json:"valor"`
}

// Ativacao é a resposta de POST /perfil/versoes/{n}/ativar.
type Ativacao struct {
	Versao     int         `json:"versao"`
	Rejeitadas []Rejeitada `json:"rejeitadas"`
}

// AtivarVersao é o fluxo T-F6: grava uma versão nova com o perfil de n e rejeita
// o que entrou depois dela, senão a próxima sessão traria de volta o que o
// operador quis desfazer (S13). O que está em n e tinha sido removido é
// restaurado, senão a próxima derivação o tiraria sem ninguém pedir (S34). Voltar
// para a versão em vigor não muda nada.
func (s *Servico) AtivarVersao(ctx context.Context, ag agentes.Agente, op Operador, n int) (Ativacao, error) {
	alvoJSON, err := s.Store.Versao(ctx, ag.ID, op.Tenant, n)
	if err != nil {
		return Ativacao{}, err
	}
	alvo, err := lerPerfil(alvoJSON)
	if err != nil {
		return Ativacao{}, err
	}
	out := Ativacao{Rejeitadas: []Rejeitada{}}
	res, err := s.Store.EditarPerfil(ctx, store.Edicao{
		Agente: ag.ID, Tenant: op.Tenant, OperadorID: op.ID, Origem: fmt.Sprintf("volta:%d", n),
		Derivar: func(obs []store.Observacao, ativoJSON []byte) (store.Derivacao, error) {
			atual, err := lerPerfil(ativoJSON)
			if err != nil {
				return store.Derivacao{}, err
			}
			mudancas := diferencas(atual, alvo)
			if len(mudancas) == 0 {
				return store.Derivacao{}, nil
			}
			diff, err := json.Marshal(mudancas)
			if err != nil {
				return store.Derivacao{}, err
			}
			d := store.Derivacao{PerfilJSON: alvoJSON, DiffJSON: diff, Mudou: true}
			for _, o := range obs {
				switch {
				case o.Estado == store.ObservacaoRejeitada && alvo.contem(o):
					d.Restaurar = append(d.Restaurar, o.ID)
				case elegivel(o) && atual.contem(o) && !alvo.contem(o):
					d.Rejeitar = append(d.Rejeitar, o.ID)
					out.Rejeitadas = append(out.Rejeitadas, Rejeitada{ID: o.ID, Campo: o.Campo, Valor: o.ValorExibicao})
				}
			}
			return d, nil
		},
	})
	if err != nil {
		return Ativacao{}, err
	}
	if res.Versao != nil {
		out.Versao = *res.Versao
		return out, nil
	}
	_, out.Versao, err = s.Store.PerfilAtivo(ctx, ag.ID, op.Tenant)
	return out, err
}

// contem diz se o padrão da observação está no perfil.
func (p Perfil) contem(o store.Observacao) bool {
	for _, c := range p.campos() {
		if c.Campo != o.Campo {
			continue
		}
		switch v := c.Depois.(type) {
		case string:
			return v == o.Valor
		case bool:
			return v == (o.Valor == "sim")
		case []string:
			return slices.Contains(v, o.ValorExibicao)
		case []Exemplo:
			return slices.ContainsFunc(v, func(e Exemplo) bool { return e.Resposta == o.ValorExibicao })
		}
	}
	return false
}

// ObservacaoResumo é uma linha de GET /observacoes.
type ObservacaoResumo struct {
	ID       string  `json:"id"`
	Campo    string  `json:"campo"`
	Valor    string  `json:"valor"`
	Origem   string  `json:"origem"`
	Estado   string  `json:"estado"`
	Sessoes  int     `json:"sessoes"`
	Elegivel bool    `json:"elegivel"`
	Citacao  *string `json:"citacao"`
}

// ListaObservacoes é a resposta de GET /observacoes.
type ListaObservacoes struct {
	Observacoes []ObservacaoResumo `json:"observacoes"`
}

func opcional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Observacoes lista os padrões do tenant; estado vazio traz todos.
func (s *Servico) Observacoes(ctx context.Context, ag agentes.Agente, op Operador, estado string) (ListaObservacoes, error) {
	if estado != "" && estado != store.ObservacaoCandidata && estado != store.ObservacaoRejeitada {
		return ListaObservacoes{}, ErrEstadoInvalido
	}
	obs, err := s.Store.Observacoes(ctx, ag.ID, op.Tenant)
	if err != nil {
		return ListaObservacoes{}, err
	}
	out := ListaObservacoes{Observacoes: []ObservacaoResumo{}}
	for _, o := range obs {
		if estado == "" || o.Estado == estado {
			out.Observacoes = append(out.Observacoes, ObservacaoResumo{ID: o.ID, Campo: o.Campo, Valor: o.ValorExibicao,
				Origem: o.Origem, Estado: o.Estado, Sessoes: o.Sessoes, Elegivel: elegivel(o), Citacao: opcional(o.Citacao)})
		}
	}
	return out, nil
}

// Edicao é a resposta de rejeitar ou restaurar uma observação.
type Edicao struct {
	VersaoNova *int      `json:"versaoNova"`
	Mudancas   []Mudanca `json:"mudancas"`
}

// MudarObservacao é o fluxo T-F7: muda o estado e deriva o perfil de novo.
func (s *Servico) MudarObservacao(ctx context.Context, ag agentes.Agente, op Operador, id, estado string) (Edicao, error) {
	out := Edicao{Mudancas: []Mudanca{}}
	e := store.Edicao{Agente: ag.ID, Tenant: op.Tenant, OperadorID: op.ID, Origem: "edicao",
		Derivar: func(todas []store.Observacao, ativoJSON []byte) (store.Derivacao, error) {
			d, m, err := derivarVersao(todas, ativoJSON)
			out.Mudancas = m
			return d, err
		}}
	if estado == store.ObservacaoRejeitada {
		e.Rejeitar = []string{id}
	} else {
		e.Restaurar = []string{id}
	}
	res, err := s.Store.EditarPerfil(ctx, e)
	if err != nil {
		return Edicao{}, err
	}
	out.VersaoNova = res.Versao
	return out, nil
}

// derivarVersao calcula o perfil das observações e o que mudou no ativo.
func derivarVersao(todas []store.Observacao, ativoJSON []byte) (store.Derivacao, []Mudanca, error) {
	atual, err := lerPerfil(ativoJSON)
	if err != nil {
		return store.Derivacao{}, nil, err
	}
	novo := derivar(todas)
	mudancas := diferencas(atual, novo)
	if len(mudancas) == 0 {
		return store.Derivacao{}, mudancas, nil
	}
	perfilJSON, err := json.Marshal(novo)
	if err != nil {
		return store.Derivacao{}, nil, err
	}
	diffJSON, err := json.Marshal(mudancas)
	if err != nil {
		return store.Derivacao{}, nil, err
	}
	return store.Derivacao{PerfilJSON: perfilJSON, DiffJSON: diffJSON, Mudou: true}, mudancas, nil
}

// NotaDetalhe é uma linha de GET /notas.
type NotaDetalhe struct {
	ID       string  `json:"id"`
	Texto    string  `json:"texto"`
	Citacao  *string `json:"citacao"`
	Estado   string  `json:"estado"`
	CriadaEm string  `json:"criadaEm"`
}

// ListaNotas é a resposta de GET /notas.
type ListaNotas struct {
	Notas []NotaDetalhe `json:"notas"`
}

// Notas lista os fatos do tenant, pendentes primeiro; estado vazio traz todos.
func (s *Servico) Notas(ctx context.Context, ag agentes.Agente, op Operador, estado string) (ListaNotas, error) {
	if estado != "" && estado != store.NotaPendente && estado != store.NotaAplicada && estado != store.NotaDescartada {
		return ListaNotas{}, ErrEstadoInvalido
	}
	notas, err := s.Store.Notas(ctx, ag.ID, op.Tenant, estado)
	if err != nil {
		return ListaNotas{}, err
	}
	out := ListaNotas{Notas: make([]NotaDetalhe, 0, len(notas))}
	for _, n := range notas {
		out.Notas = append(out.Notas, NotaDetalhe{ID: n.ID, Texto: n.Texto, Citacao: opcional(n.Citacao), Estado: n.Estado, CriadaEm: iso(n.CriadaEm)})
	}
	return out, nil
}

// NotaMarcada é a resposta de POST /notas/{id}/marcar.
type NotaMarcada struct {
	ID     string `json:"id"`
	Estado string `json:"estado"`
}

// MarcarNota é o fluxo T-F8. O agens só registra: quem aplica o fato é o sistema,
// dono da configuração (S12). Nota já resolvida não muda; repetir é seguro,
// porque o sistema reenvia quando a marcação falha (contrato 10, C34).
func (s *Servico) MarcarNota(ctx context.Context, ag agentes.Agente, op Operador, id, estado string) (NotaMarcada, error) {
	if estado != store.NotaAplicada && estado != store.NotaDescartada {
		return NotaMarcada{}, ErrEstadoInvalido
	}
	atual, err := s.Store.MarcarNota(ctx, ag.ID, op.Tenant, id, estado, s.agora())
	if err != nil {
		return NotaMarcada{}, err
	}
	return NotaMarcada{ID: id, Estado: atual}, nil
}

// Zeramento é a resposta de DELETE /perfil.
type Zeramento struct {
	Versao int `json:"versao"`
}

// Zerar é o fluxo T-F9: versão vazia, tudo rejeitado, notas pendentes descartadas.
// Sessões e mensagens ficam para auditoria até a retenção.
func (s *Servico) Zerar(ctx context.Context, ag agentes.Agente, op Operador) (Zeramento, error) {
	res, err := s.Store.EditarPerfil(ctx, store.Edicao{
		Agente: ag.ID, Tenant: op.Tenant, OperadorID: op.ID, Origem: "zerado",
		RejeitarTodas: true, DescartarNotas: true, Em: s.agora(),
		Derivar: func(_ []store.Observacao, ativoJSON []byte) (store.Derivacao, error) {
			atual, err := lerPerfil(ativoJSON)
			if err != nil {
				return store.Derivacao{}, err
			}
			vazio := novoPerfil()
			perfilJSON, err := json.Marshal(vazio)
			if err != nil {
				return store.Derivacao{}, err
			}
			diffJSON, err := json.Marshal(diferencas(atual, vazio))
			if err != nil {
				return store.Derivacao{}, err
			}
			return store.Derivacao{PerfilJSON: perfilJSON, DiffJSON: diffJSON, Mudou: true}, nil
		},
	})
	if err != nil {
		return Zeramento{}, err
	}
	return Zeramento{Versao: *res.Versao}, nil
}
