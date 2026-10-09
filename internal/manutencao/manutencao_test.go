package manutencao

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
)

// contador embrulha o Store real para contar e falhar o expurgo.
type contador struct {
	store.Store
	expurgos []time.Time
	eventos  []time.Time
	falha    error
}

func (c *contador) ExpurgarTexto(ctx context.Context, antes time.Time) (int64, error) {
	if c.falha != nil {
		return 0, c.falha
	}
	c.expurgos = append(c.expurgos, antes)
	return c.Store.ExpurgarTexto(ctx, antes)
}

func (c *contador) ApagarEventosEcos(ctx context.Context, antes time.Time) (int64, error) {
	c.eventos = append(c.eventos, antes)
	return c.Store.ApagarEventosEcos(ctx, antes)
}

var loc = time.FixedZone("BRT", -3*3600)

func montar(t *testing.T, agora *time.Time) (*Runner, *contador) {
	t.Helper()
	clock := func() time.Time { return *agora }
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "agens.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := &contador{Store: s}
	return New(c, clock, loc, 4, 90, slog.New(slog.NewTextHandler(io.Discard, nil))), c
}

func dia(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, loc) }

func TestCatchUpTresDiasUmaVezCada(t *testing.T) {
	agora := dia(5, 5) // dia 5, depois das 04:00, nada executado
	r, c := montar(t, &agora)
	ctx := context.Background()

	r.Tick(ctx)
	if len(c.expurgos) != 3 {
		t.Fatalf("execuções = %d, quer 3 (dias 3, 4 e 5)", len(c.expurgos))
	}
	for _, ref := range []string{"2026-10-03", "2026-10-04", "2026-10-05"} {
		if ok, _ := c.JobFeito(ctx, "retencao", ref); !ok {
			t.Errorf("%s não marcado", ref)
		}
	}
	if ok, _ := c.JobFeito(ctx, "retencao", "2026-10-02"); ok {
		t.Error("dia 2 está fora da janela de 3 dias")
	}
	// prazos relativos ao agora
	if !c.expurgos[0].Equal(agora.AddDate(0, 0, -90)) || !c.eventos[0].Equal(agora.Add(-7*24*time.Hour)) {
		t.Errorf("prazos: expurgo=%v eventos=%v", c.expurgos[0], c.eventos[0])
	}

	r.Tick(ctx)
	if len(c.expurgos) != 3 {
		t.Errorf("segundo tick rodou de novo: %d", len(c.expurgos))
	}
	// no dia seguinte roda só o dia novo
	agora = dia(6, 4)
	r.Tick(ctx)
	if len(c.expurgos) != 4 {
		t.Errorf("dia seguinte: %d execuções, quer 4", len(c.expurgos))
	}
}

func TestAntesDaHoraNaoRodaHoje(t *testing.T) {
	agora := dia(5, 3) // 03:00 < 04:00
	r, c := montar(t, &agora)
	r.Tick(context.Background())
	if len(c.expurgos) != 2 {
		t.Fatalf("execuções = %d, quer 2 (dias 3 e 4)", len(c.expurgos))
	}
	if ok, _ := c.JobFeito(context.Background(), "retencao", "2026-10-05"); ok {
		t.Error("hoje não deveria ter rodado antes da hora")
	}
	agora = dia(5, 4)
	r.Tick(context.Background())
	if len(c.expurgos) != 3 {
		t.Errorf("na hora certa deveria rodar hoje: %d", len(c.expurgos))
	}
}

func TestFusoDefineODia(t *testing.T) {
	// 02:00 UTC do dia 6 ainda é dia 5 às 23:00 em BRT (hora 4 já passou no dia 5)
	agora := time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC)
	r, c := montar(t, &agora)
	r.Tick(context.Background())
	if ok, _ := c.JobFeito(context.Background(), "retencao", "2026-10-05"); !ok {
		t.Error("data_ref deveria ser 2026-10-05 no fuso")
	}
	if ok, _ := c.JobFeito(context.Background(), "retencao", "2026-10-06"); ok {
		t.Error("2026-10-06 ainda não começou no fuso")
	}
}

func TestErroNaoMarca(t *testing.T) {
	agora := dia(5, 5)
	r, c := montar(t, &agora)
	ctx := context.Background()
	c.falha = errors.New("disco cheio")
	r.Tick(ctx)
	for _, ref := range []string{"2026-10-03", "2026-10-04", "2026-10-05"} {
		if ok, _ := c.JobFeito(ctx, "retencao", ref); ok {
			t.Errorf("%s marcado apesar do erro", ref)
		}
	}
	c.falha = nil
	r.Tick(ctx)
	if len(c.expurgos) != 3 {
		t.Errorf("depois do erro deveria rodar os 3 dias: %d", len(c.expurgos))
	}
}

func TestStartRodaUmaVezAoSubirEParaComContexto(t *testing.T) {
	agora := dia(5, 5)
	r, c := montar(t, &agora)
	ctx, cancel := context.WithCancel(context.Background())
	feito := make(chan struct{})
	go func() { r.Start(ctx); close(feito) }()
	deadline := time.After(2 * time.Second)
	for {
		if ok, _ := c.JobFeito(context.Background(), "retencao", "2026-10-05"); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("não rodou ao subir")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-feito:
	case <-time.After(2 * time.Second):
		t.Fatal("Start não parou com o contexto")
	}
}
