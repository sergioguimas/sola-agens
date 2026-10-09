package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/sistema"
)

var agoraFixo = time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)

func (a *amb) deMim(id, texto string) {
	a.t.Helper()
	a.motor.Clock = func() time.Time { return agoraFixo }
	if err := a.motor.DeMim(context.Background(), a.ag, conversaDeTeste, mensagem.Mensagem{ID: id, Texto: texto}); err != nil {
		a.t.Fatal(err)
	}
}

func (a *amb) pausadaAte() *time.Time {
	a.t.Helper()
	c, err := a.st.Conversa(context.Background(), conversaDeTeste)
	if err != nil {
		a.t.Fatal(err)
	}
	return c.PausadaAte
}

func TestDeMimEcoEhIgnorado(t *testing.T) {
	a := novoAmb(t)
	if err := a.st.RegistrarEco(context.Background(), "eliza", "out-1"); err != nil {
		t.Fatal(err)
	}
	a.deMim("out-1", "Olá!")
	if p := a.pausadaAte(); p != nil {
		t.Errorf("eco pausou a conversa até %v", p)
	}
	if len(a.janela()) != 0 {
		t.Errorf("eco foi para a memória: %v", a.janela())
	}
	contem(t, a.logs.String(), `"msg":"eco"`)
}

func TestDeMimPausaPeloPadraoEGravaComoHumano(t *testing.T) {
	a := novoAmb(t)
	a.deMim("h-1", "Oi, aqui é a Bia")
	p := a.pausadaAte()
	if p == nil || !p.Equal(agoraFixo.Add(12*time.Hour)) {
		t.Fatalf("pausada_ate = %v, quer agora+12h", p)
	}
	if want := []string{"humano:Oi, aqui é a Bia"}; !reflect.DeepEqual(a.janela(), want) {
		t.Errorf("memória = %v", a.janela())
	}
	if len(a.st.turnos) != 0 {
		t.Error("deMim não gera turno")
	}
	// a próxima mensagem do cliente não chama o LLM
	a.motor.Clock = nil
	// (relógio real: a pausa de 12 h a partir de agoraFixo já passou; refaz com futuro)
	a.st.Pausar(context.Background(), conversaDeTeste, time.Now().Add(time.Hour))
	a.run("e aí?")
	a.semLLM()
}

func TestDeMimUsaPausaHumanaHorasDaConfigGuardada(t *testing.T) {
	a := novoAmb(t)
	h := 2
	cfg, _ := json.Marshal(sistema.Atendente{PausaHumanaHoras: &h})
	if err := a.st.GuardarConfig(context.Background(), conversaDeTeste, cfg); err != nil {
		t.Fatal(err)
	}
	a.deMim("h-1", "")
	p := a.pausadaAte()
	if p == nil || !p.Equal(agoraFixo.Add(2*time.Hour)) {
		t.Fatalf("pausada_ate = %v, quer agora+2h", p)
	}
	if len(a.janela()) != 0 {
		t.Errorf("sem texto não grava memória: %v", a.janela())
	}
}

func TestDeMimPadraoDoAmbienteQuandoNaoHaConfig(t *testing.T) {
	a := novoAmb(t)
	a.motor.Config.PausaHumanaH = 5
	a.deMim("h-1", "x")
	if p := a.pausadaAte(); p == nil || !p.Equal(agoraFixo.Add(5*time.Hour)) {
		t.Fatalf("pausada_ate = %v, quer agora+5h", p)
	}
}

func TestDeMimNovaMensagemReiniciaOPrazo(t *testing.T) {
	a := novoAmb(t)
	a.deMim("h-1", "um")
	a.motor.Clock = func() time.Time { return agoraFixo.Add(time.Hour) }
	a.motor.DeMim(context.Background(), a.ag, conversaDeTeste, mensagem.Mensagem{ID: "h-2", Texto: "dois"})
	if p := a.pausadaAte(); p == nil || !p.Equal(agoraFixo.Add(13*time.Hour)) {
		t.Fatalf("pausada_ate = %v, quer agora+1h+12h", p)
	}
}
