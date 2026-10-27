package agent

import (
	"reflect"
	"testing"

	"sola-agens/internal/llm"
)

func TestFormatarWhatsApp(t *testing.T) {
	casos := []struct{ nome, entra, sai string }{
		{"negrito duplo", "Está **pendente de aprovação**.", "Está *pendente de aprovação*."},
		{"vários na linha", "**Corte** com **Bia**", "*Corte* com *Bia*"},
		{"negrito simples fica", "Está *pendente*.", "Está *pendente*."},
		{"sem marcação", "Oi! Como posso ajudar?", "Oi! Como posso ajudar?"},
		{"sem par fica", "Preço **R$ 60", "Preço **R$ 60"},
		{"não atravessa linha", "**Corte\nBia**", "**Corte\nBia**"},
		{"multiplicação solta", "2 * 3 = 6", "2 * 3 = 6"},
		{"vazio", "", ""},
	}
	for _, c := range casos {
		if got := FormatarWhatsApp(c.entra); got != c.sai {
			t.Errorf("%s: FormatarWhatsApp(%q) = %q, quer %q", c.nome, c.entra, got, c.sai)
		}
	}
}

func TestRespostaComNegritoDuploSaiNoFormatoDoWhatsApp(t *testing.T) {
	a := novoAmb(t)
	a.llm.Respostas = []llm.GenerateResponse{llm.TextoResp("Seu agendamento está **pendente de aprovação**.")}
	a.run("pode ser às 9")
	quer := "Seu agendamento está *pendente de aprovação*."
	if !reflect.DeepEqual(a.sis.Enviadas, []string{quer}) {
		t.Errorf("enviadas = %v", a.sis.Enviadas)
	}
	j := a.janela()
	if len(j) == 0 || j[len(j)-1] != "agente:"+quer {
		t.Errorf("memória = %v, quer a última como o texto enviado", j)
	}
}
