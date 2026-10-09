package transcricao

import (
	"context"
	"errors"
	"testing"
)

func TestFake_Roteirizado(t *testing.T) {
	f := &Fake{Textos: []string{"primeiro", "segundo"}}

	t1, _, err := f.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if err != nil || t1 != "primeiro" {
		t.Fatalf("chamada 1 = (%q, %v)", t1, err)
	}
	t2, _, err := f.Transcrever(context.Background(), []byte{0x02}, "audio/ogg", "pt")
	if err != nil || t2 != "segundo" {
		t.Fatalf("chamada 2 = (%q, %v)", t2, err)
	}

	if len(f.Chamadas) != 2 {
		t.Fatalf("Chamadas = %d, want 2", len(f.Chamadas))
	}
	if f.Chamadas[0].Mimetype != "audio/ogg" || f.Chamadas[0].Idioma != "pt" {
		t.Errorf("Chamadas[0] = %+v", f.Chamadas[0])
	}
}

func TestFake_ErroRoteirizado(t *testing.T) {
	erroFixo := errors.New("falha simulada")
	f := &Fake{Erros: []error{erroFixo}}

	_, _, err := f.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt")
	if !errors.Is(err, erroFixo) {
		t.Fatalf("err = %v, want %v", err, erroFixo)
	}
}

func TestFake_SemRoteiroDaErroClaro(t *testing.T) {
	f := &Fake{}
	if _, _, err := f.Transcrever(context.Background(), []byte{0x01}, "audio/ogg", "pt"); err == nil {
		t.Fatal("esperava erro de roteiro incompleto")
	}
}
