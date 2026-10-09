package agent

import (
	"context"
	"encoding/json"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/sistema"
	"sola-agens/internal/store"
)

// DeMim trata a mensagem enviada pelo número do estabelecimento (specs/03 F3):
// eco do próprio agens é ignorado; senão a conversa pausa por pausaHumanaHoras
// (da última configuração guardada, ou o padrão do ambiente) e o texto, se
// houver, entra na memória como papel humano. Não gera turno.
func (m *Motor) DeMim(ctx context.Context, ag *Agente, id store.ConversaID, msg mensagem.Mensagem) error {
	log := m.log().With("agente", id.Agente, "tenant", id.Tenant, "conversa", mensagem.HashContato(id.Contato))

	eco, err := m.Store.EhEco(ctx, ag.Registro.ID, msg.ID)
	if err != nil {
		return err
	}
	if eco {
		log.Info("eco")
		return nil
	}

	horas := m.padrao().PausaHumanaHoras
	conv, err := m.Store.Conversa(ctx, id)
	if err != nil {
		return err
	}
	if len(conv.ConfigJSON) > 0 {
		var a sistema.Atendente
		if json.Unmarshal(conv.ConfigJSON, &a) == nil && a.PausaHumanaHoras != nil && *a.PausaHumanaHoras > 0 {
			horas = *a.PausaHumanaHoras
		}
	}
	ate := m.agora().Add(time.Duration(horas) * time.Hour)
	if err := m.Store.Pausar(ctx, id, ate); err != nil {
		return err
	}
	if msg.Texto != "" {
		t := msg.Texto
		if err := m.Store.AnexarMensagem(ctx, id, store.MensagemMemoria{Papel: store.PapelHumano, Conteudo: &t}); err != nil {
			log.Error("memoria_nao_gravada", "erro", err.Error())
		}
	}
	log.Info("pausa", "ate", ate.UTC().Format(time.RFC3339))
	return nil
}
