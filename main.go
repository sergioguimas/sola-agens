// Comando sola-agens: motor de atendimento por LLM da Sola (specs/05).
// Fase 1: esqueleto — config, agentes e HTTP; o handler de mensagens é um no-op.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"sola-agens/internal/agentes"
	"sola-agens/internal/config"
	"sola-agens/internal/entrada"
	"sola-agens/internal/httpapi"
)

// noopHandler só registra a entrada; o worker real entra nas fases seguintes.
// Nunca loga texto, ticket nem telefone (conversa = hash do contato).
type noopHandler struct{ log *slog.Logger }

func (h noopHandler) registrar(msg string, ag agentes.Agente, e entrada.Entrada) {
	tipo := "texto"
	if len(e.Mensagem.Anexos) > 0 {
		tipo = e.Mensagem.Anexos[0].Tipo
	}
	h.log.Info(msg, "agente", ag.ID, "tenant", e.Tenant, "conversa", e.Conversa(),
		"conteudo", tipo, "deMim", e.DeMim)
}

func (h noopHandler) Mensagem(_ context.Context, ag agentes.Agente, e entrada.Entrada) error {
	h.registrar("mensagem recebida", ag, e)
	return nil
}

func (h noopHandler) DeMim(_ context.Context, ag agentes.Agente, e entrada.Entrada) error {
	h.registrar("mensagem deMim recebida", ag, e)
	return nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("encerrado com erro", "erro", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// .env é opcional (em produção as variáveis vêm do container).
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cfg, err := config.LoadConfigFrom(os.LookupEnv)
	if err != nil {
		return err
	}
	cfg.Precos = config.PrecosBrutos(os.Environ())

	if cfg.LogDebug {
		log = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	ags, err := agentes.Carregar(cfg.AgentsDir, os.LookupEnv)
	if err != nil {
		return err
	}
	if len(ags) == 0 {
		return errors.New("nenhum agente em " + cfg.AgentsDir)
	}
	for _, a := range ags {
		log.Info("agente carregado", "agente", a)
	}

	api := httpapi.New(ags, noopHandler{log: log}, log)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("sola-agens no ar", "listen", cfg.Listen)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("encerrando")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shCtx)
}
