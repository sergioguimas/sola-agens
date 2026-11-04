package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"sola-agens/internal/agent"
	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
)

// Web atende o canal web (specs/07). Síncrono: a resposta vai no corpo.
type Web interface {
	TurnoWeb(ctx context.Context, ag agentes.Agente, m entrada.MensagemWeb) (entrada.RespostaWeb, error)
}

// web é POST /a/{agente}/v1/web. Assinado como a entrada encaminhada, com o mesmo
// segredo: o Eliza já é quem fala com o motor neste canal (contrato 08 §6.2).
func (s *Server) web(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agente")
	log := s.log.With("agente", id, "rota", "web")
	ag, ok := s.agente(id)
	if !ok || ag.Web == nil || s.CanalWeb == nil {
		erroTreino(w, http.StatusNotFound, "WEB_DESLIGADO", "canal web desligado para este agente")
		return
	}
	// Turno síncrono, maior que o WriteTimeout do servidor (S22).
	prazo := time.Duration(ag.Web.TurnoTimeoutS)*time.Second + folgaEscrita
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(prazo))

	corpo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxCorpo))
	if err != nil {
		erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "corpo ilegível")
		return
	}
	err = entrada.VerificarAssinatura([]byte(ag.Segredo()), r.Header.Get(ag.Entrada.HeaderTimestamp), corpo,
		r.Header.Get(ag.Entrada.HeaderAssinatura), s.Agora())
	if err != nil {
		log.Warn("web rejeitado", "motivo", "assinatura", "detalhe", err.Error())
		erroTreino(w, http.StatusUnauthorized, "NAO_AUTORIZADO", "não autorizado")
		return
	}
	m, err := entrada.LerMensagemWeb(corpo)
	if err != nil {
		erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "corpo inválido")
		return
	}
	if s.Eventos != nil {
		novo, err := s.Eventos.MarcarEvento(r.Context(), id, m.MensagemID)
		if err != nil {
			log.Error("falha no dedupe", "erro", err.Error())
			erroTreino(w, http.StatusInternalServerError, "ERRO_INTERNO", "erro interno")
			return
		}
		if !novo {
			erroTreino(w, http.StatusConflict, "DUPLICADA", "mensagem repetida")
			return
		}
	}
	res, err := s.CanalWeb.TurnoWeb(r.Context(), ag, m)
	switch {
	case errors.Is(err, agent.ErrTetoWeb):
		log.Warn("web: teto do dia", "tenant", m.OrgID)
		erroTreino(w, http.StatusTooManyRequests, "TETO_DIARIO", "teto do dia atingido")
		return
	case err != nil:
		erroTreino(w, http.StatusServiceUnavailable, "INDISPONIVEL", "canal indisponível")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Warn("web: resposta não escrita", "erro", err.Error())
	}
}
