package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

// Headers da identidade nas rotas sem corpo (specs/06 §4).
const (
	headerTenant = "X-Agens-Tenant"
	headerID     = "X-Agens-Operador-Id"
	headerPapel  = "X-Agens-Operador-Papel"
)

// pedidoTreino é o corpo das rotas de treino (specs/06 §5.1). ticketVisitante
// chega aqui quando o sistema tiver a F2a e ainda é ignorado.
type pedidoTreino struct {
	Operador treino.Operador `json:"operador"`
	Texto    string          `json:"texto"`
	Estado   string          `json:"estado"`
}

type acaoTreino func(*http.Request, agentes.Agente, pedidoTreino) (any, error)

func (s *Server) rotasTreino(mux *http.ServeMux) {
	const base = "/a/{agente}/v1/treino"
	rota := func(metodo, caminho string, acao acaoTreino) {
		mux.HandleFunc(metodo+" "+base+caminho, s.comTreino(acao))
	}
	t := func() *treino.Servico { return s.Treino }

	rota("POST", "/sessoes", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Abrir(r.Context(), ag, p.Operador)
	})
	rota("POST", "/sessoes/{id}/mensagens", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Mensagem(r.Context(), ag, p.Operador, r.PathValue("id"), p.Texto)
	})
	rota("POST", "/sessoes/{id}/encerrar", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Encerrar(r.Context(), ag, p.Operador, r.PathValue("id"))
	})
	rota("GET", "/perfil", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().LerPerfil(r.Context(), ag, p.Operador)
	})
	rota("DELETE", "/perfil", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Zerar(r.Context(), ag, p.Operador)
	})
	rota("GET", "/perfil/versoes", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Versoes(r.Context(), ag, p.Operador)
	})
	rota("POST", "/perfil/versoes/{n}/ativar", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil {
			return nil, store.ErrNaoEncontrado
		}
		return t().AtivarVersao(r.Context(), ag, p.Operador, n)
	})
	rota("GET", "/observacoes", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Observacoes(r.Context(), ag, p.Operador, r.URL.Query().Get("estado"))
	})
	rota("POST", "/observacoes/{id}/rejeitar", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().MudarObservacao(r.Context(), ag, p.Operador, r.PathValue("id"), store.ObservacaoRejeitada)
	})
	rota("POST", "/observacoes/{id}/restaurar", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().MudarObservacao(r.Context(), ag, p.Operador, r.PathValue("id"), store.ObservacaoCandidata)
	})
	rota("GET", "/notas", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().Notas(r.Context(), ag, p.Operador, r.URL.Query().Get("estado"))
	})
	rota("POST", "/notas/{id}/marcar", func(r *http.Request, ag agentes.Agente, p pedidoTreino) (any, error) {
		return t().MarcarNota(r.Context(), ag, p.Operador, r.PathValue("id"), p.Estado)
	})
}

// mensagemAssinada monta o que a assinatura cobre (specs/06 §4, S32): método e
// caminho entram para uma assinatura não valer em outra rota. O caminho é o que
// vem depois de /v1/treino, com a query, porque é o que o sistema conhece.
func mensagemAssinada(r *http.Request, agente string, corpo []byte) []byte {
	caminho := strings.TrimPrefix(r.URL.RequestURI(), "/a/"+agente+"/v1/treino")
	conteudo := string(corpo)
	if len(corpo) == 0 {
		conteudo = r.Header.Get(headerTenant) + "." + r.Header.Get(headerID) + "." + r.Header.Get(headerPapel)
	}
	return []byte(r.Method + "." + caminho + "." + conteudo)
}

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
			mensagemAssinada(r, id, corpo), r.Header.Get(ag.Treino.HeaderAssinatura), s.Agora())
		if err != nil {
			log.Warn("treino rejeitado", "motivo", "assinatura", "detalhe", err.Error())
			erroTreino(w, http.StatusUnauthorized, "NAO_AUTORIZADO", "não autorizado")
			return
		}
		var p pedidoTreino
		if len(corpo) == 0 {
			p.Operador = treino.Operador{Tenant: r.Header.Get(headerTenant), ID: r.Header.Get(headerID), Papel: r.Header.Get(headerPapel)}
		} else if err := json.Unmarshal(corpo, &p); err != nil {
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
		res, err := acao(r, ag, p)
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
		erroTreino(w, http.StatusNotFound, "NAO_ENCONTRADO", "não encontrado")
	case errors.Is(err, store.ErrSessaoEncerrada):
		erroTreino(w, http.StatusConflict, "SESSAO_ENCERRADA", "sessão encerrada")
	case errors.Is(err, treino.ErrSessaoCheia):
		erroTreino(w, http.StatusConflict, "SESSAO_CHEIA", "sessão cheia")
	case errors.Is(err, treino.ErrTextoLongo):
		erroTreino(w, http.StatusUnprocessableEntity, "TEXTO_LONGO", "texto longo demais")
	case errors.Is(err, treino.ErrTextoVazio), errors.Is(err, treino.ErrEstadoInvalido):
		erroTreino(w, http.StatusBadRequest, "CORPO_INVALIDO", "pedido inválido")
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
