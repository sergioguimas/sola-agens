// Comando sola-agens: motor de atendimento por LLM da Sola (specs/05).
// Fases 1–4: config, agentes, OpenAPI → ferramentas, HTTP, store (dedupe,
// apagamento), manutenção e o agente (debounce → fila por conversa → LLM).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"sola-agens/internal/agent"
	"sola-agens/internal/atendimento"
	"sola-agens/internal/config"
	"sola-agens/internal/httpapi"
	"sola-agens/internal/llm"
	"sola-agens/internal/llm/gemini"
	"sola-agens/internal/manutencao"
	"sola-agens/internal/store/sqlite"
	"sola-agens/internal/trava/memoria"
)

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
	if err := llm.ConfigurarPrecos(cfg.Precos); err != nil {
		return err
	}

	// Boot (specs/03 F6): registro de agentes, OpenAPI baixada e convertida,
	// operações do motor localizadas. Qualquer falha derruba o processo antes
	// de o HTTP subir.
	fab := &atendimento.Fabrica{
		Dir:    cfg.AgentsDir,
		Lookup: os.LookupEnv,
		// DECISÃO: timeout de 30 s por tentativa de download (a spec é maior que um
		// request comum; AGENS_SISTEMA_TIMEOUT vale para as chamadas do turno).
		Cliente: &http.Client{Timeout: 30 * time.Second},
		Timeout: cfg.SistemaTimeout,
		Modelo:  cfg.Model,
		Log:     log,
	}
	conj, err := fab.Montar(context.Background())
	if err != nil {
		return err
	}
	for _, a := range conj.Registro {
		log.Info("agente carregado", "agente", a)
	}

	st, err := sqlite.Open(filepath.Join(cfg.DataDir, "agens.db"), time.Now)
	if err != nil {
		return err
	}
	defer st.Close()

	motor := &agent.Motor{
		Store: st,
		LLM:   gemini.NewClient(cfg.GeminiAPIKey, nil, log),
		Log:   log,
		Config: agent.Config{
			Temperature: cfg.Temperature, ThinkingBudget: cfg.ThinkingBudget, PausaHumanaH: cfg.PausaHumanaH,
		},
	}
	svc := atendimento.Novo(atendimento.Opcoes{
		Conjunto: conj, Fabrica: fab, Runner: motor, Trava: &memoria.Trava{},
		MaxConversas: cfg.MaxConversas, Log: log,
	})

	api := httpapi.New(conj.Registro, svc, log)
	svc.AoTrocar = api.Trocar
	api.Eventos = st
	api.AdminToken, api.Apagador = cfg.AdminToken, st
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Manutenção (F9): roda ao subir e a cada minuto até o ctx do processo acabar.
	var manut sync.WaitGroup
	manut.Add(1)
	go func() {
		defer manut.Done()
		manutencao.New(st, time.Now, cfg.Loc, cfg.ManutencaoHora, cfg.RetencaoDias, log).Start(ctx)
	}()

	// SIGHUP (F6): recarrega agentes/*.yaml e as OpenAPIs; inválido mantém o anterior.
	if sinais := sinaisRecarga(); len(sinais) > 0 {
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, sinais...)
		defer signal.Stop(hup)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-hup:
					log.Info("recarregando agentes")
					rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					_ = svc.Recarregar(rctx) // o erro já foi logado e o conjunto anterior ficou
					cancel()
				}
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("sola-agens no ar", "listen", cfg.Listen)

	var errServidor error
	select {
	case errServidor = <-errCh:
	case <-ctx.Done():
	}

	// Encerramento (F6): HTTP → debounce → worker → manutenção → store (defer).
	log.Info("encerrando")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errShutdown := srv.Shutdown(shCtx)
	if err := svc.Close(time.Duration(cfg.EncerramentoS) * time.Second); err != nil {
		log.Error("encerramento da fila", "erro", err.Error())
	}
	stop()
	manut.Wait()

	if errServidor != nil && !errors.Is(errServidor, http.ErrServerClosed) {
		return errServidor
	}
	return errShutdown
}
