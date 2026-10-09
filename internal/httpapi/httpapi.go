// Package httpapi expõe /health e a entrada encaminhada
// POST /a/{agente}/v1/mensagens (specs/03 F1, specs/04 §1).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/entrada"
	"sola-agens/internal/store"
)

// MaxCorpo é o limite do corpo aceito (F1 passo 2).
const MaxCorpo = 64 << 10

// ErrFilaCheia é devolvido pelo Handler quando a fila da conversa está cheia;
// o servidor responde 503 (F1 passo 9).
var ErrFilaCheia = errors.New("fila da conversa cheia")

// Handler recebe a entrada já autenticada e normalizada. É chamado de forma
// SÍNCRONA dentro da requisição, então deve ser rápido: só enfileirar. O
// processamento (LLM, sistema) é da fila/worker, nunca daqui — o Eliza aborta
// em 3 s.
type Handler interface {
	Mensagem(ctx context.Context, ag agentes.Agente, e entrada.Entrada) error
	DeMim(ctx context.Context, ag agentes.Agente, e entrada.Entrada) error
}

// Eventos é o pedaço do Store que o dedupe usa (F1 passos 6 e 9).
// DECISÃO: interface pequena local no lugar da DedupeFunc, para o handler poder
// desfazer a marca (DesmarcarEvento) sem um segundo callback.
type Eventos interface {
	MarcarEvento(ctx context.Context, agente, mensagemID string) (novo bool, err error)
	DesmarcarEvento(ctx context.Context, agente, mensagemID string) error
}

// Apagador é o pedaço do Store que a rota de apagamento usa (F10).
type Apagador interface {
	ApagarContato(ctx context.Context, id store.ConversaID) error
}

// Server monta as rotas.
type Server struct {
	agentes atomic.Pointer[map[string]agentes.Agente] // trocado pelo SIGHUP (Trocar)
	handler Handler
	log     *slog.Logger

	// Eventos é opcional: nil = sem deduplicação.
	Eventos Eventos
	// AdminToken e Apagador ligam DELETE /admin/agentes/{a}/tenants/{t}/contatos/{c};
	// token vazio (ou Apagador nil) = rota não registrada.
	AdminToken string
	Apagador   Apagador
	// Agora existe para os testes fixarem o relógio.
	Agora func() time.Time
}

// New cria o servidor.
func New(ags []agentes.Agente, h Handler, log *slog.Logger) *Server {
	s := &Server{handler: h, log: log, Agora: time.Now}
	s.Trocar(ags)
	return s
}

// Trocar substitui o registro de agentes (recarga por SIGHUP, specs/03 F6);
// requisições em andamento seguem com o conjunto que já leram.
func (s *Server) Trocar(ags []agentes.Agente) {
	m := make(map[string]agentes.Agente, len(ags))
	for _, a := range ags {
		m[a.ID] = a
	}
	s.agentes.Store(&m)
}

func (s *Server) agente(id string) (agentes.Agente, bool) {
	a, ok := (*s.agentes.Load())[id]
	return a, ok
}

// Routes devolve o mux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("POST /a/{agente}/v1/mensagens", s.mensagens)
	if s.AdminToken != "" && s.Apagador != nil {
		mux.HandleFunc("DELETE /admin/agentes/{a}/tenants/{t}/contatos/{c}", s.apagarContato)
	}
	return mux
}

func (s *Server) mensagens(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agente")
	log := s.log.With("agente", id)

	// 1. agente não registrado
	ag, ok := s.agente(id)
	if !ok {
		log.Warn("agente desconhecido")
		http.Error(w, "agente desconhecido", http.StatusNotFound)
		return
	}

	// 2. corpo > 64 KB
	corpo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxCorpo))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			log.Warn("corpo grande demais")
			http.Error(w, "corpo grande demais", http.StatusRequestEntityTooLarge)
			return
		}
		log.Warn("erro lendo corpo")
		http.Error(w, "corpo ilegível", http.StatusBadRequest)
		return
	}

	// 3 e 4. timestamp e assinatura
	err = entrada.VerificarAssinatura(
		[]byte(ag.Segredo()),
		r.Header.Get(ag.Entrada.HeaderTimestamp),
		corpo,
		r.Header.Get(ag.Entrada.HeaderAssinatura),
		s.Agora(),
	)
	if err != nil {
		motivo := "assinatura"
		if errors.Is(err, entrada.ErrTimestampAusente) || errors.Is(err, entrada.ErrTimestampInvalido) ||
			errors.Is(err, entrada.ErrTimestampForaJanela) {
			motivo = "timestamp"
		}
		log.Warn("entrada rejeitada", "motivo", motivo, "detalhe", err.Error())
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}

	// 5. JSON inválido ou versao != 1
	var msg entrada.MensagemEncaminhada
	if err := json.Unmarshal(corpo, &msg); err != nil {
		log.Warn("json inválido")
		http.Error(w, "json inválido", http.StatusBadRequest)
		return
	}
	e, err := entrada.Normalizar(msg)
	if err != nil {
		log.Warn("corpo inválido", "detalhe", err.Error())
		http.Error(w, "corpo inválido", http.StatusBadRequest)
		return
	}
	log = log.With("tenant", e.Tenant, "conversa", e.Conversa())

	// 6. dedupe: MarcarEvento(agente, mensagemId); já existia → 202 com log
	// "duplicado" (202, não 204: o Eliza trata outro código como falha).
	if s.Eventos != nil {
		novo, err := s.Eventos.MarcarEvento(r.Context(), id, e.Mensagem.ID)
		if err != nil {
			log.Error("falha no dedupe", "erro", err.Error())
			http.Error(w, "erro interno", http.StatusInternalServerError)
			return
		}
		if !novo {
			log.Info("duplicado")
			aceito(w)
			return
		}
	}

	// 7. mensagem enviada pelo número do estabelecimento (F3)
	if e.DeMim {
		s.entregar(w, r, log, s.handler.DeMim, ag, e)
		return
	}

	// 8. sem ticket não há como agir em nome do cliente (contrato quebrado)
	if e.Ticket == "" {
		log.Warn("ticket ausente")
		http.Error(w, "ticket ausente", http.StatusBadRequest)
		return
	}

	// 9. entrega ao debounce/fila
	s.entregar(w, r, log, s.handler.Mensagem, ag, e)
}

func (s *Server) entregar(w http.ResponseWriter, r *http.Request, log *slog.Logger,
	fn func(context.Context, agentes.Agente, entrada.Entrada) error, ag agentes.Agente, e entrada.Entrada) {
	if err := fn(r.Context(), ag, e); err != nil {
		// DECISÃO: qualquer falha de entrega (não só fila cheia) desfaz a marca,
		// senão o reenvio do sistema viraria "duplicado" e a mensagem se perderia.
		if s.Eventos != nil {
			if derr := s.Eventos.DesmarcarEvento(r.Context(), ag.ID, e.Mensagem.ID); derr != nil {
				log.Error("falha ao desmarcar evento", "erro", derr.Error())
			}
		}
		if errors.Is(err, ErrFilaCheia) {
			log.Warn("fila cheia")
			http.Error(w, "fila cheia", http.StatusServiceUnavailable)
			return
		}
		log.Error("falha no handler", "erro", err.Error())
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}
	aceito(w)
}

func aceito(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	io.WriteString(w, `{"aceito":true}`)
}
