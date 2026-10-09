package agent

import (
	"sola-agens/internal/sistema"
)

// fastpath resolve "sim"/"cancelar" soltos sem LLM (specs/02 §6). Só roda com
// uma mensagem de texto, texto normalizado inteiro igual a uma palavra e
// exatamente um agendamento aguardando confirmação. As operações vêm de
// operacoes.confirmar/cancelar do registro: sem elas, a intenção fica desligada.
//
// Devolve aplicou=false para seguir ao LLM (inclusive quando a operação volta
// com erro de regra); falha != nil é erro técnico (F2).
func (e *turnoExec) fastpath(c sistema.Contexto) (texto string, aplicou bool, f *falha) {
	if len(e.t.Mensagens) != 1 {
		return "", false, nil
	}
	m := e.t.Mensagens[0]
	if m.Texto == "" || len(m.Anexos) > 0 || len(c.AguardandoConfirmacao) != 1 {
		return "", false, nil
	}

	n := Normalizar(m.Texto)
	var operacao, resposta string
	switch {
	case contemPalavra(e.dic.Palavras.Confirmar, n):
		operacao, resposta = e.ag.Registro.Operacoes.Confirmar, e.dic.Respostas.Confirmado
	case contemPalavra(e.dic.Palavras.Cancelar, n):
		operacao, resposta = e.ag.Registro.Operacoes.Cancelar, e.dic.Respostas.Cancelado
	}
	if operacao == "" {
		return "", false, nil
	}
	ferramenta, ok := e.ag.Ferramenta(operacao)
	if !ok {
		e.log.Warn("fastpath_sem_ferramenta", "operacao", operacao)
		return "", false, nil
	}

	r, f := e.executarFerramenta(ferramenta, map[string]any{"id": c.AguardandoConfirmacao[0]})
	if f != nil {
		return "", false, f
	}
	if r.ErroRegra != nil { // o LLM sabe explicar (ex.: dentro da antecedência mínima)
		e.log.Info("fastpath_recusado", "codigo", r.ErroRegra.Erro)
		return "", false, nil
	}
	return Aplicar(resposta, e.nome), true, nil
}
