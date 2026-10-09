package memoria

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestExclusaoMutuaNaMesmaChave(t *testing.T) {
	var tr Trava
	ctx := context.Background()
	var dentro, maximo, total int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			liberar, err := tr.Adquirir(ctx, "a")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			dentro++
			total++
			if dentro > maximo {
				maximo = dentro
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			dentro--
			mu.Unlock()
			liberar()
		}()
	}
	wg.Wait()
	if maximo != 1 || total != 20 {
		t.Errorf("máximo simultâneo=%d total=%d", maximo, total)
	}
	if len(tr.chaves) != 0 {
		t.Errorf("mapa não foi limpo: %d chaves", len(tr.chaves))
	}
}

func TestChavesDiferentesNaoBloqueiam(t *testing.T) {
	var tr Trava
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a, err := tr.Adquirir(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := tr.Adquirir(ctx, "b")
	if err != nil {
		t.Fatalf("chave b bloqueada por a: %v", err)
	}
	a()
	b()
}

func TestContextoCancelado(t *testing.T) {
	var tr Trava
	liberar, err := tr.Adquirir(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	// já cancelado
	cancelado, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.Adquirir(cancelado, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("ctx já cancelado: %v", err)
	}
	// cancelado enquanto espera
	ctx, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if _, err := tr.Adquirir(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("esperando: %v", err)
	}
	// quem desistiu não deixa a chave presa
	liberar()
	liberar() // idempotente
	l2, err := tr.Adquirir(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	l2()
	if len(tr.chaves) != 0 {
		t.Errorf("mapa não foi limpo: %d chaves", len(tr.chaves))
	}
}
