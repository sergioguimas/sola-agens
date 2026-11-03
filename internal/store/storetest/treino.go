package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sola-agens/internal/store"
)

// StoreTreinoFechavel é o que a bateria do treino precisa da implementação.
type StoreTreinoFechavel interface {
	store.StoreTreino
	Close() error
}

func sessaoNova(operador string) store.SessaoNova {
	return store.SessaoNova{Agente: "eliza", Tenant: "org-1", OperadorID: operador,
		RoteiroJSON: []byte(`{"feitos":{}}`), InicioDia: passado, LimiteDia: 10}
}

// RodarTreino é a bateria de contrato de store.StoreTreino (specs/06 §14).
func RodarTreino(t *testing.T, nova func(t *testing.T) StoreTreinoFechavel) {
	ctx := context.Background()

	t.Run("abrir duas vezes devolve a mesma sessão", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a, novaA, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil || !novaA {
			t.Fatalf("primeira: nova=%v err=%v", novaA, err)
		}
		b, novaB, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil || novaB || b.ID != a.ID {
			t.Fatalf("segunda: id=%s nova=%v err=%v (quer %s)", b.ID, novaB, err, a.ID)
		}
		c, _, err := s.AbrirSessao(ctx, sessaoNova("u2"))
		if err != nil || c.ID == a.ID {
			t.Fatalf("outro operador: id=%s err=%v", c.ID, err)
		}
	})

	t.Run("abertura concorrente cria uma só", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		var wg sync.WaitGroup
		ids := make([]string, 8)
		for i := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				x, _, err := s.AbrirSessao(ctx, sessaoNova("u1"))
				if err != nil {
					t.Error(err)
				}
				ids[i] = x.ID
			}()
		}
		wg.Wait()
		for _, id := range ids {
			if id != ids[0] {
				t.Fatalf("ids diferentes: %v", ids)
			}
		}
	})

	t.Run("limite do dia não barra quem já tem sessão aberta", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		n := sessaoNova("u1")
		n.LimiteDia = 1
		a, _, err := s.AbrirSessao(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		if _, nv, err := s.AbrirSessao(ctx, n); err != nil || nv {
			t.Fatalf("reabrir no limite: nova=%v err=%v", nv, err)
		}
		n.OperadorID = "u2"
		if _, _, err := s.AbrirSessao(ctx, n); !errors.Is(err, store.ErrLimiteSessoes) {
			t.Fatalf("outro operador no limite: %v", err)
		}
		// sessões de antes do início do dia não contam
		n.InicioDia = futuro
		if _, _, err := s.AbrirSessao(ctx, n); err != nil {
			t.Fatalf("dia novo: %v", err)
		}
		if err := s.EncerrarSessao(ctx, a.ID, base); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("sessão de outro tenant ou agente não existe", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a, _, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SessaoTreino(ctx, "eliza", "org-2", a.ID); !errors.Is(err, store.ErrNaoEncontrado) {
			t.Errorf("outro tenant: %v", err)
		}
		if _, err := s.SessaoTreino(ctx, "site", "org-1", a.ID); !errors.Is(err, store.ErrNaoEncontrado) {
			t.Errorf("outro agente: %v", err)
		}
		if _, err := s.SessaoTreino(ctx, "eliza", "org-1", "nada"); !errors.Is(err, store.ErrNaoEncontrado) {
			t.Errorf("inexistente: %v", err)
		}
		got, err := s.SessaoTreino(ctx, "eliza", "org-1", a.ID)
		if err != nil || got.Estado != store.SessaoAberta || got.OperadorID != "u1" {
			t.Fatalf("mesma org: %+v err=%v", got, err)
		}
	})

	t.Run("turno grava falas em ordem e o roteiro", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a, _, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil {
			t.Fatal(err)
		}
		msgs := []store.MensagemTreino{
			{Papel: store.PapelOperador, Tipo: store.TipoRespostaEnsaio, Cenario: "preco", Conteudo: ptr("sai 150")},
			{Papel: store.PapelAgente, Tipo: store.TipoEnsaio, Cenario: "atraso", Conteudo: ptr(`"atrasei"`)},
		}
		if err := s.GravarTurnoTreino(ctx, a.ID, msgs, []byte(`{"feitos":{"atraso":1}}`)); err != nil {
			t.Fatal(err)
		}
		got, err := s.MensagensTreino(ctx, a.ID)
		if err != nil || len(got) != 2 {
			t.Fatalf("mensagens: %d err=%v", len(got), err)
		}
		if got[0].Tipo != store.TipoRespostaEnsaio || got[0].Cenario != "preco" || *got[0].Conteudo != "sai 150" {
			t.Errorf("primeira: %+v", got[0])
		}
		if got[1].Papel != store.PapelAgente || got[1].Cenario != "atraso" {
			t.Errorf("segunda: %+v", got[1])
		}
		l, err := s.SessaoTreino(ctx, "eliza", "org-1", a.ID)
		if err != nil || string(l.RoteiroJSON) != `{"feitos":{"atraso":1}}` {
			t.Errorf("roteiro: %s err=%v", l.RoteiroJSON, err)
		}
	})

	t.Run("sessão encerrada não aceita turno e abrir cria outra", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a, _, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil {
			t.Fatal(err)
		}
		em := base.Add(time.Hour)
		if err := s.EncerrarSessao(ctx, a.ID, em); err != nil {
			t.Fatal(err)
		}
		// encerrar de novo não muda o instante
		if err := s.EncerrarSessao(ctx, a.ID, em.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		l, err := s.SessaoTreino(ctx, "eliza", "org-1", a.ID)
		if err != nil || l.Estado != store.SessaoEncerrada || l.EncerradaEm == nil || !l.EncerradaEm.Equal(em) {
			t.Fatalf("encerrada: %+v err=%v", l, err)
		}
		err = s.GravarTurnoTreino(ctx, a.ID, []store.MensagemTreino{
			{Papel: store.PapelOperador, Tipo: store.TipoLivre, Conteudo: ptr("oi")}}, []byte(`{}`))
		if !errors.Is(err, store.ErrSessaoEncerrada) {
			t.Fatalf("turno em encerrada: %v", err)
		}
		if m, _ := s.MensagensTreino(ctx, a.ID); len(m) != 0 {
			t.Errorf("gravou %d falas numa sessão encerrada", len(m))
		}
		b, nv, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil || !nv || b.ID == a.ID {
			t.Fatalf("nova depois de encerrar: id=%s nova=%v err=%v", b.ID, nv, err)
		}
	})
}
