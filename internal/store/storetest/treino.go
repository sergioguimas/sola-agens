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
	encerrada := func(t *testing.T, s StoreTreinoFechavel, tenant, operador string) store.SessaoTreino {
		t.Helper()
		n := sessaoNova(operador)
		n.Tenant = tenant
		x, _, err := s.AbrirSessao(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EncerrarSessao(ctx, x.ID, base); err != nil {
			t.Fatal(err)
		}
		return x
	}
	semMudanca := func([]store.Observacao, []byte) (store.Derivacao, error) { return store.Derivacao{}, nil }
	extracao := func(x store.SessaoTreino, obs ...store.ObservacaoNova) store.Extracao {
		return store.Extracao{Agente: x.Agente, Tenant: x.Tenant, SessaoID: x.ID, OperadorID: x.OperadorID,
			Observacoes: obs, Derivar: semMudanca}
	}
	saudacao := store.ObservacaoNova{Campo: "saudacoes", Valor: "oi", ValorExibicao: "Oi", Origem: store.OrigemExtraida,
		Citacao: "Oi", Ocorrencias: 2}

	t.Run("fusão soma evidência entre sessões, não dentro da mesma", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a := encerrada(t, s, "org-1", "u1")
		res, err := s.AplicarExtracao(ctx, extracao(a, saudacao, saudacao))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Observacoes) != 1 || res.Observacoes[0].Sessoes != 1 || res.Observacoes[0].Ocorrencias != 4 {
			t.Fatalf("primeira: %+v", res.Observacoes)
		}
		b := encerrada(t, s, "org-1", "u1")
		res, err = s.AplicarExtracao(ctx, extracao(b, saudacao))
		if err != nil {
			t.Fatal(err)
		}
		o := res.Observacoes[0]
		if o.Sessoes != 2 || o.Ocorrencias != 6 || o.UltimaSessao != b.ID || o.ValorExibicao != "Oi" {
			t.Fatalf("segunda: %+v", o)
		}
	})

	t.Run("declaração promove a observação", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		if _, err := s.AplicarExtracao(ctx, extracao(encerrada(t, s, "org-1", "u1"), saudacao)); err != nil {
			t.Fatal(err)
		}
		decl := saudacao
		decl.Origem = store.OrigemDeclarada
		res, err := s.AplicarExtracao(ctx, extracao(encerrada(t, s, "org-1", "u1"), decl))
		if err != nil || res.Observacoes[0].Origem != store.OrigemDeclarada {
			t.Fatalf("%+v err=%v", res.Observacoes, err)
		}
	})

	t.Run("sessão extraída não aplica de novo", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a := encerrada(t, s, "org-1", "u1")
		if _, err := s.AplicarExtracao(ctx, extracao(a, saudacao)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AplicarExtracao(ctx, extracao(a, saudacao)); !errors.Is(err, store.ErrJaExtraida) {
			t.Fatalf("segunda aplicação: %v", err)
		}
		l, err := s.SessaoTreino(ctx, "eliza", "org-1", a.ID)
		if err != nil || l.Estado != store.SessaoExtraida || l.Tentativas != 1 {
			t.Fatalf("sessão: %+v err=%v", l, err)
		}
	})

	t.Run("falha ao derivar desfaz tudo", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a := encerrada(t, s, "org-1", "u1")
		e := extracao(a, saudacao)
		e.Notas = []store.NotaNova{{ID: "n1", Texto: "abre domingo"}}
		e.Derivar = func([]store.Observacao, []byte) (store.Derivacao, error) {
			return store.Derivacao{}, errors.New("falhou")
		}
		if _, err := s.AplicarExtracao(ctx, e); err == nil {
			t.Fatal("esperava erro")
		}
		// A observação e a nota não ficaram: reaplicar conta uma sessão só.
		e.Derivar = semMudanca
		res, err := s.AplicarExtracao(ctx, e)
		if err != nil {
			t.Fatalf("reaplicar: %v (nota duplicada?)", err)
		}
		if res.Observacoes[0].Ocorrencias != 2 {
			t.Fatalf("evidência dobrou: %+v", res.Observacoes[0])
		}
	})

	t.Run("versão nova fica ativa e só uma por tenant", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		mudou := func(perfil string) func([]store.Observacao, []byte) (store.Derivacao, error) {
			return func([]store.Observacao, []byte) (store.Derivacao, error) {
				return store.Derivacao{PerfilJSON: []byte(perfil), DiffJSON: []byte(`[]`), Mudou: true}, nil
			}
		}
		for i, perfil := range []string{`{"v":1}`, `{"v":2}`} {
			e := extracao(encerrada(t, s, "org-1", "u1"))
			e.Derivar = mudou(perfil)
			res, err := s.AplicarExtracao(ctx, e)
			if err != nil || res.Versao == nil || *res.Versao != i+1 {
				t.Fatalf("versão %d: %+v err=%v", i+1, res.Versao, err)
			}
		}
		raw, v, err := s.PerfilAtivo(ctx, "eliza", "org-1")
		if err != nil || v != 2 || string(raw) != `{"v":2}` {
			t.Fatalf("ativo: v%d %s err=%v", v, raw, err)
		}
		// O perfil chega à derivação seguinte, e outro tenant não o vê.
		e := extracao(encerrada(t, s, "org-1", "u1"))
		e.Derivar = func(_ []store.Observacao, ativo []byte) (store.Derivacao, error) {
			if string(ativo) != `{"v":2}` {
				t.Errorf("ativo na derivação: %s", ativo)
			}
			return store.Derivacao{}, nil
		}
		if _, err := s.AplicarExtracao(ctx, e); err != nil {
			t.Fatal(err)
		}
		raw, v, err = s.PerfilAtivo(ctx, "eliza", "org-2")
		if err != nil || v != 0 || raw != nil {
			t.Fatalf("outro tenant: v%d %s err=%v", v, raw, err)
		}
	})

	t.Run("observações não atravessam tenant", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		if _, err := s.AplicarExtracao(ctx, extracao(encerrada(t, s, "org-1", "u1"), saudacao)); err != nil {
			t.Fatal(err)
		}
		res, err := s.AplicarExtracao(ctx, extracao(encerrada(t, s, "org-2", "u9")))
		if err != nil || len(res.Observacoes) != 0 {
			t.Fatalf("outro tenant: %+v err=%v", res.Observacoes, err)
		}
	})

	t.Run("leitura de observações e notas fica no tenant", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		e := extracao(encerrada(t, s, "org-1", "u1"), saudacao)
		e.Notas = []store.NotaNova{{ID: "n1", Texto: "abre domingo"}}
		if _, err := s.AplicarExtracao(ctx, e); err != nil {
			t.Fatal(err)
		}
		obs, err := s.Observacoes(ctx, "eliza", "org-1")
		if err != nil || len(obs) != 1 || obs[0].Valor != "oi" {
			t.Fatalf("observações: %+v err=%v", obs, err)
		}
		notas, err := s.Notas(ctx, "eliza", "org-1", store.NotaPendente)
		if err != nil || len(notas) != 1 || notas[0].Texto != "abre domingo" {
			t.Fatalf("notas: %+v err=%v", notas, err)
		}
		if o, _ := s.Observacoes(ctx, "eliza", "org-2"); len(o) != 0 {
			t.Errorf("observações de outro tenant: %+v", o)
		}
		if n, _ := s.Notas(ctx, "eliza", "org-2", ""); len(n) != 0 {
			t.Errorf("notas de outro tenant: %+v", n)
		}
	})

	t.Run("edição muda estado, grava versão e desfaz tudo se falhar", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		res, err := s.AplicarExtracao(ctx, extracao(encerrada(t, s, "org-1", "u1"), saudacao))
		if err != nil {
			t.Fatal(err)
		}
		id := res.Observacoes[0].ID
		falha := errors.New("falhou")
		_, err = s.EditarPerfil(ctx, store.Edicao{Agente: "eliza", Tenant: "org-1", OperadorID: "u1", Origem: "edicao", Rejeitar: []string{id},
			Derivar: func([]store.Observacao, []byte) (store.Derivacao, error) { return store.Derivacao{}, falha }})
		if !errors.Is(err, falha) {
			t.Fatalf("erro: %v", err)
		}
		if o, _ := s.Observacoes(ctx, "eliza", "org-1"); o[0].Estado != store.ObservacaoCandidata {
			t.Fatalf("rejeição ficou depois da falha: %+v", o[0])
		}
		r, err := s.EditarPerfil(ctx, store.Edicao{Agente: "eliza", Tenant: "org-1", OperadorID: "u1", Origem: "edicao", Rejeitar: []string{id},
			Derivar: func(obs []store.Observacao, _ []byte) (store.Derivacao, error) {
				if obs[0].Estado != store.ObservacaoRejeitada {
					t.Errorf("derivação viu o estado antigo: %+v", obs[0])
				}
				return store.Derivacao{PerfilJSON: []byte(`{}`), DiffJSON: []byte(`[]`), Mudou: true}, nil
			}})
		if err != nil || r.Versao == nil || *r.Versao != 1 {
			t.Fatalf("edição: %+v err=%v", r.Versao, err)
		}
		vs, err := s.Versoes(ctx, "eliza", "org-1")
		if err != nil || len(vs) != 1 || vs[0].Origem != "edicao" || !vs[0].Ativa {
			t.Fatalf("versões: %+v err=%v", vs, err)
		}
		if raw, err := s.Versao(ctx, "eliza", "org-1", 1); err != nil || string(raw) != `{}` {
			t.Fatalf("versão 1: %s err=%v", raw, err)
		}
		if _, err := s.Versao(ctx, "eliza", "org-1", 9); !errors.Is(err, store.ErrNaoEncontrado) {
			t.Errorf("versão inexistente: %v", err)
		}
		if _, err := s.Versao(ctx, "eliza", "org-2", 1); !errors.Is(err, store.ErrNaoEncontrado) {
			t.Errorf("versão de outro tenant: %v", err)
		}
	})

	t.Run("edição não alcança observação de outro tenant", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		res, err := s.AplicarExtracao(ctx, extracao(encerrada(t, s, "org-1", "u1"), saudacao))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.EditarPerfil(ctx, store.Edicao{Agente: "eliza", Tenant: "org-2", OperadorID: "u9", Origem: "edicao",
			Rejeitar: []string{res.Observacoes[0].ID}, Derivar: semMudanca})
		if !errors.Is(err, store.ErrNaoEncontrado) {
			t.Fatalf("outro tenant: %v", err)
		}
		if o, _ := s.Observacoes(ctx, "eliza", "org-1"); o[0].Estado != store.ObservacaoCandidata {
			t.Fatalf("outro tenant rejeitou: %+v", o[0])
		}
	})

	t.Run("zerar rejeita tudo e descarta as pendentes do tenant", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		for _, tenant := range []string{"org-1", "org-2"} {
			e := extracao(encerrada(t, s, tenant, "u1"), saudacao)
			e.Notas = []store.NotaNova{{ID: "n-" + tenant, Texto: "fato"}}
			if _, err := s.AplicarExtracao(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		_, err := s.EditarPerfil(ctx, store.Edicao{Agente: "eliza", Tenant: "org-1", OperadorID: "u1", Origem: "zerado",
			RejeitarTodas: true, DescartarNotas: true, Em: base, Derivar: semMudanca})
		if err != nil {
			t.Fatal(err)
		}
		if o, _ := s.Observacoes(ctx, "eliza", "org-1"); o[0].Estado != store.ObservacaoRejeitada {
			t.Errorf("observação: %+v", o[0])
		}
		if n, _ := s.Notas(ctx, "eliza", "org-1", ""); n[0].Estado != store.NotaDescartada {
			t.Errorf("nota: %+v", n[0])
		}
		if o, _ := s.Observacoes(ctx, "eliza", "org-2"); o[0].Estado != store.ObservacaoCandidata {
			t.Errorf("outro tenant foi zerado: %+v", o[0])
		}
		if n, _ := s.Notas(ctx, "eliza", "org-2", store.NotaPendente); len(n) != 1 {
			t.Errorf("nota de outro tenant: %+v", n)
		}
	})

	t.Run("marcar nota é repetível e não troca a decisão", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		e := extracao(encerrada(t, s, "org-1", "u1"))
		e.Notas = []store.NotaNova{{ID: "n1", Texto: "fato"}}
		if _, err := s.AplicarExtracao(ctx, e); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MarcarNota(ctx, "eliza", "org-2", "n1", store.NotaAplicada, base); !errors.Is(err, store.ErrNaoEncontrado) {
			t.Fatalf("outro tenant: %v", err)
		}
		for i := 0; i < 2; i++ {
			if v, err := s.MarcarNota(ctx, "eliza", "org-1", "n1", store.NotaAplicada, base); err != nil || v != store.NotaAplicada {
				t.Fatalf("marcar %d: %s err=%v", i, v, err)
			}
		}
		if v, err := s.MarcarNota(ctx, "eliza", "org-1", "n1", store.NotaDescartada, base); err != nil || v != store.NotaAplicada {
			t.Fatalf("decisão trocada: %s err=%v", v, err)
		}
		if n, _ := s.Notas(ctx, "eliza", "org-1", store.NotaPendente); len(n) != 0 {
			t.Errorf("ainda pendente: %+v", n)
		}
	})

	t.Run("sessão aberta não é extraída", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a, _, err := s.AbrirSessao(ctx, sessaoNova("u1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AplicarExtracao(ctx, extracao(a)); err == nil {
			t.Fatal("esperava erro")
		}
	})

	t.Run("pendentes respeitam as tentativas e inativas o prazo", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a := encerrada(t, s, "org-1", "u1")
		ids := func(l []store.SessaoTreino) int { return len(l) }
		l, err := s.SessoesPendentes(ctx, "eliza", 2)
		if err != nil || ids(l) != 1 || l[0].ID != a.ID {
			t.Fatalf("pendentes: %+v err=%v", l, err)
		}
		for i := 0; i < 2; i++ {
			if err := s.MarcarErroExtracao(ctx, a.ID); err != nil {
				t.Fatal(err)
			}
		}
		if l, _ = s.SessoesPendentes(ctx, "eliza", 2); ids(l) != 0 {
			t.Fatalf("depois de 2 falhas: %+v", l)
		}
		b, _, err := s.AbrirSessao(ctx, sessaoNova("u2"))
		if err != nil {
			t.Fatal(err)
		}
		if l, _ = s.SessoesInativas(ctx, "eliza", b.UltimaMsgEm); ids(l) != 0 {
			t.Fatalf("no limite ainda não é inativa: %+v", l)
		}
		if l, _ = s.SessoesInativas(ctx, "eliza", b.UltimaMsgEm.Add(time.Second)); ids(l) != 1 {
			t.Fatalf("inativas: %+v", l)
		}
		if l, _ = s.SessoesInativas(ctx, "site", futuro); ids(l) != 0 {
			t.Fatalf("outro agente: %+v", l)
		}
	})
}
