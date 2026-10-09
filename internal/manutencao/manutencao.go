// Package manutencao roda o job diário de retenção (specs/03 F9), no padrão do
// scheduler do Smaug: tick de 60 s, catch-up de 3 dias e (nome, data_ref) só
// marcado depois de sucesso.
package manutencao

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sola-agens/internal/store"
)

const (
	tickIntervalo = 60 * time.Second
	// janelaCatchUp conta hoje: hoje, ontem e anteontem.
	janelaCatchUp = 3
	// RetencaoEventos é o prazo de eventos e ecos (specs/01 §9).
	RetencaoEventos = 7 * 24 * time.Hour
)

// Runner executa os jobs de manutenção.
type Runner struct {
	Store        store.Store
	Clock        func() time.Time
	Loc          *time.Location
	Hora         int // AGENS_MANUTENCAO_HORA, no fuso Loc
	RetencaoDias int // AGENS_RETENCAO_DIAS
	Log          *slog.Logger

	jobs []job
}

type job struct {
	nome string
	run  func(ctx context.Context, dia time.Time) error
}

// New monta o Runner com o job "retencao". clock nil = time.Now; loc nil = UTC.
func New(st store.Store, clock func() time.Time, loc *time.Location, hora, retencaoDias int, log *slog.Logger) *Runner {
	if clock == nil {
		clock = time.Now
	}
	if loc == nil {
		loc = time.UTC
	}
	if log == nil {
		log = slog.Default()
	}
	r := &Runner{Store: st, Clock: clock, Loc: loc, Hora: hora, RetencaoDias: retencaoDias, Log: log}
	r.jobs = []job{{nome: "retencao", run: r.retencao}}
	return r
}

// retencao zera o texto antigo e apaga eventos e ecos velhos. Os prazos são
// relativos ao agora, não ao dia que o catch-up está processando.
func (r *Runner) retencao(ctx context.Context, dia time.Time) error {
	agora := r.Clock()
	textos, err := r.Store.ExpurgarTexto(ctx, agora.AddDate(0, 0, -r.RetencaoDias))
	if err != nil {
		return fmt.Errorf("expurgar texto: %w", err)
	}
	eventos, err := r.Store.ApagarEventosEcos(ctx, agora.Add(-RetencaoEventos))
	if err != nil {
		return fmt.Errorf("apagar eventos e ecos: %w", err)
	}
	r.Log.Info("manutenção", "job", "retencao", "dia", dia.Format("2006-01-02"),
		"mensagens_expurgadas", textos, "eventos_ecos_apagados", eventos)
	return nil
}

// Tick faz uma passada: para cada dia de [hoje-2 .. hoje] cuja hora já chegou
// e que ainda não foi marcado, roda o job. Erro só loga; tenta no próximo tick.
func (r *Runner) Tick(ctx context.Context) {
	agora := r.Clock().In(r.Loc)
	for _, j := range r.jobs {
		for offset := janelaCatchUp - 1; offset >= 0; offset-- {
			if ctx.Err() != nil {
				return
			}
			a, m, d := agora.AddDate(0, 0, -offset).Date()
			dia := time.Date(a, m, d, 0, 0, 0, 0, r.Loc)
			if agora.Before(time.Date(a, m, d, r.Hora, 0, 0, 0, r.Loc)) {
				continue
			}
			dataRef := dia.Format("2006-01-02")
			feito, err := r.Store.JobFeito(ctx, j.nome, dataRef)
			if err != nil {
				r.Log.Error("manutenção", "job", j.nome, "dia", dataRef, "acao", "erro_checar", "erro", err.Error())
				continue
			}
			if feito {
				continue
			}
			if err := j.run(ctx, dia); err != nil {
				r.Log.Error("manutenção", "job", j.nome, "dia", dataRef, "acao", "erro_run", "erro", err.Error())
				continue
			}
			if err := r.Store.MarcarJob(ctx, j.nome, dataRef); err != nil {
				r.Log.Error("manutenção", "job", j.nome, "dia", dataRef, "acao", "erro_marcar", "erro", err.Error())
			}
		}
	}
}

// Start roda um Tick já e depois a cada 60 s, até ctx terminar. Devolve quando
// para, para o chamador poder esperar o encerramento.
func (r *Runner) Start(ctx context.Context) {
	r.Tick(ctx)
	t := time.NewTicker(tickIntervalo)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Tick(ctx)
		}
	}
}
