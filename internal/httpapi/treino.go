package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
	"sola-agens/internal/store"
	"sola-agens/internal/treino"
)

// folgaEscrita é o que o servidor espera além do prazo do turno para escrever
// a resposta.
const folgaEscrita = 5 * time.Second

// pedidoTreino é o corpo das rotas de treino (specs/06 §5.1). ticketVisitante
// chega aqui quando o sistema tiver a F2a e ainda é ignorado.
type pedidoTreino struct {
	Operador treino.Operador `json:"operador"`
	Texto    string          `json:"texto"`
}

func (s *Server) rotasTreino(mux *http.ServeMux) {
	const base = "POST /a/{agente}/v1/treino/sessoes"
	mux.HandleFunc(base, s.comTreino(func(w http.ResponseWriter, r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return s.Treino.Abrir(r.Context(), ag, p.Operador)
	}))
	mux.HandleFunc(base+"/{id}/mensagens", s.comTreino(func(w http.ResponseWriter, r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return s.Treino.Mensagem(r.Context(), ag, p.Operador, r.PathValue("id"), p.Texto)
	}))
	mux.HandleFunc(base+"/{id}/encerrar", s.comTreino(func(w http.ResponseWriter, r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return s.Treino.Encerrar(r.Context(), ag, p.Operador, r.PathValue("id"))
	}))
}

type acaoTreino func(http.ResponseWriter, *http.Request, agentes.Agente, pedidoTreino) (any, error)

// comTreino faz o que é comum às rotas: agente com treino ligado, assinatura,
// operador e papel (specs/06 §4), nessa ordem, e traduz o resultado.
func (s *Server) comTreino(acao acaoTreino) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("agente")
		log := s.log.With("agente", id, "rota", "treino")
		ag, ok := s.agente(id)
		if !ok || ag.Treino == nil || s.Treino == nil {
			erroTreino(w, http.StatusNotFound, "TREINO_DESLIGADO", "treino desligado para este agente")
			return
		}
		// O turno é síncrono e passa do WriteTimeout do servidor (15 s, S22).
		// httptest não implementa o deadline: o erro só importa em produção,
		// onde o servidor real sempre aceita.
		prazo := time.Duration(ag.Treino.Limites.TurnoTimeoutS)*time.Second + folgaEscrita
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(prazo))

		corpo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxCorpo))
		if err != nil {
			var grande *http.MaxBytesError
			if errors.As(err, &grande) {
				erroTreino(w, http.StatusRequestEntityTooLarge, "CORPO_GRANDE", "corpo grande demais")
				return
			}
			erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "corpo ilegível")
			return
		}
		err = entrada.VerificarAssinatura([]byte(ag.SegredoTreino()), r.Header.Get(ag.Treino.HeaderTimestamp),
			corpo, r.Header.Get(ag.Treino.HeaderAssinatura), s.Agora())
		if err != nil {
			log.Warn("treino rejeitado", "motivo", "assinatura", "detalhe", err.Error())
			erroTreino(w, http.StatusUnauthorized, "NAO_AUTORIZADO", "não autorizado")
			return
		}
		var p pedidoTreino
		if err := json.Unmarshal(corpo, &p); err != nil {
			erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "json inválido")
			return
		}
		if strings.TrimSpace(p.Operador.Tenant) == "" || strings.TrimSpace(p.Operador.ID) == "" {
			erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "operador incompleto")
			return
		}
		log = log.With("tenant", p.Operador.Tenant)
		if !ag.Treino.PapelPermitido(p.Operador.Papel) {
			log.Warn("treino rejeitado", "motivo", "papel", "papel", p.Operador.Papel)
			erroTreino(w, http.StatusForbidden, "PAPEL_NAO_PERMITIDO", "papel sem permissão para treinar")
			return
		}
		res, err := acao(w, r, ag, p)
		if err != nil {
			responderErroTreino(w, log, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Warn("treino: resposta não escrita", "erro", err.Error())
		}
	}
}

// responderErroTreino traduz os erros do serviço (specs/06 §5, §11).
func responderErroTreino(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, store.ErrNaoEncontrado):
		erroTreino(w, http.StatusNotFound, "NAO_ENCONTRADO", "sessão não encontrada")
	case errors.Is(err, store.ErrSessaoEncerrada):
		erroTreino(w, http.StatusConflict, "SESSAO_ENCERRADA", "sessão encerrada")
	case errors.Is(err, treino.ErrSessaoCheia):
		erroTreino(w, http.StatusConflict, "SESSAO_CHEIA", "sessão cheia")
	case errors.Is(err, treino.ErrTextoLongo):
		erroTreino(w, http.StatusUnprocessableEntity, "TEXTO_LONGO", "texto longo demais")
	case errors.Is(err, treino.ErrTextoVazio):
		erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "texto vazio")
	case errors.Is(err, store.ErrLimiteSessoes):
		erroTreino(w, http.StatusTooManyRequests, "LIMITE_SESSOES", "limite de sessões do dia")
	case errors.Is(err, treino.ErrIndisponivel), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		erroTreino(w, http.StatusServiceUnavailable, "INDISPONIVEL", "treino indisponível")
	default:
		log.Error("treino: erro interno", "erro", err.Error())
		erroTreino(w, http.StatusInternalServerError, "ERRO_INTERNO", "erro interno")
	}
}

func erroTreino(w http.ResponseWriter, status int, codigo, mensagem string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"erro": map[string]string{"codigo": codigo, "mensagem": mensagem}})
}
