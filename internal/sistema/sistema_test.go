package sistema

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
)

var ops = agentes.Operacoes{
	Contexto: "obterContexto", Responder: "enviarMensagem", Escalar: "escalarParaEquipe", Midia: "baixarMidia",
}

func lerSpec(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../openapi/testdata/eliza.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// exemploContexto tira o `data` do exemplo "identificado" da própria spec do Eliza.
func exemploContexto(t *testing.T) map[string]any {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(lerSpec(t), &spec); err != nil {
		t.Fatal(err)
	}
	var atual any = spec
	for _, k := range []string{"paths", "/api/v1/autoatendimento/contexto", "get", "responses", "200", "content",
		"application/json", "examples", "identificado", "value", "data"} {
		atual = atual.(map[string]any)[k]
	}
	ex := atual
	return ex.(map[string]any)
}

func novoSistema(t *testing.T, h http.HandlerFunc) (*Real, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ex := conector.NovoHTTP(conector.Config{
		BaseURL: srv.URL, Token: "tok", HeaderTicket: "X-Autoatendimento-Ticket",
		HeadersFixos: map[string]string{"X-Autoatendimento-Versao": "1"},
	}, 2*time.Second)
	ex.Cliente = srv.Client()
	ex.Esperar = func(context.Context, time.Duration) error { return nil }
	s, err := Novo(ex, lerSpec(t), ops)
	if err != nil {
		t.Fatal(err)
	}
	return s, srv
}

func envData(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func envErro(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": "m", "request_id": "r1"}})
}

func TestContextoReal(t *testing.T) {
	ex := exemploContexto(t)
	s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/autoatendimento/contexto" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Autoatendimento-Ticket") != "tk" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("headers: %v", r.Header)
		}
		envData(w, ex)
	})
	c, err := s.Contexto(context.Background(), conector.Identidade{Ticket: "tk"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Agora.Local != "2026-10-02T10:00" || c.FusoHorario != "America/Sao_Paulo" {
		t.Errorf("agora/fuso = %+v %q", c.Agora, c.FusoHorario)
	}
	if c.Organizacao.Nome != "Studio Exemplo" || c.Organizacao.Termos.Genero != "m" || c.Organizacao.Termos.ServicoPlural != "serviços" {
		t.Errorf("organizacao = %+v", c.Organizacao)
	}
	if len(c.Expediente.DiasDaSemana) != 6 || *c.Expediente.Abertura != "09:00" || c.Expediente.Almoco == nil || c.Expediente.Almoco.Fim != "13:00" {
		t.Errorf("expediente = %+v", c.Expediente)
	}
	if c.Politica.MaxAgendamentosAtivos != 3 || c.Politica.InstrucoesAtendimento != nil {
		t.Errorf("politica = %+v", c.Politica)
	}
	if c.Identificacao.Situacao != SituacaoIdentificado || c.Identificacao.Cliente == nil || c.Identificacao.Cliente.PrimeiroNome != "Maria" {
		t.Errorf("identificacao = %+v", c.Identificacao)
	}
	if len(c.Agendamentos) != 1 || c.Agendamentos[0].Servico.Nome != "Corte de cabelo" || !c.Agendamentos[0].PodeConfirmar || c.Agendamentos[0].Inicio.Local != "2026-10-05T14:30" {
		t.Errorf("agendamentos = %+v", c.Agendamentos)
	}
	if len(c.AguardandoConfirmacao) != 1 || c.AguardandoConfirmacao[0] != c.Agendamentos[0].ID {
		t.Errorf("aguardando = %v", c.AguardandoConfirmacao)
	}
	a := c.Atendente
	if a.Persona == nil || *a.Persona != "Lia" || a.PausaHumanaHoras != nil || a.Respostas.FalhaTecnica != nil ||
		a.Respostas.Confirmado == nil || len(a.Palavras.Confirmar) != 2 || a.Palavras.Cancelar != nil {
		t.Errorf("atendente = %+v", a)
	}
}

func TestContextoSemAtendenteECampoDesconhecido(t *testing.T) {
	ex := exemploContexto(t)
	delete(ex, "atendente")
	ex["campoNovoDoEliza"] = map[string]any{"x": 1}
	ex["identificacao"] = map[string]any{"situacao": "desconhecido"}
	s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) { envData(w, ex) })
	c, err := s.Contexto(context.Background(), conector.Identidade{Ticket: "tk"})
	if err != nil {
		t.Fatal(err)
	}
	zero := Atendente{}
	if c.Atendente.Persona != zero.Persona || c.Atendente.Palavras.Confirmar != nil || c.Atendente.PausaHumanaHoras != nil {
		t.Errorf("atendente deveria ser zero value: %+v", c.Atendente)
	}
	if c.Identificacao.Situacao != SituacaoDesconhecido || c.Identificacao.Cliente != nil {
		t.Errorf("identificacao = %+v", c.Identificacao)
	}
}

func TestResponder(t *testing.T) {
	cases := []struct {
		nome string
		data any
		want string
	}{
		{"com id", map[string]any{"mensagemId": "3EB0A1"}, "3EB0A1"},
		{"id null", map[string]any{"mensagemId": nil}, ""},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.Path != "/api/v1/autoatendimento/mensagens" || string(b) != `{"texto":"olá"}` {
					t.Errorf("%s %s %s", r.Method, r.URL.Path, b)
				}
				envData(w, c.data)
			})
			got, err := s.Responder(context.Background(), conector.Identidade{Ticket: "tk"}, "olá")
			if err != nil || got != c.want {
				t.Errorf("got=%q err=%v", got, err)
			}
		})
	}
}

func TestEscalar(t *testing.T) {
	for _, notificada := range []bool{true, false} {
		s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if r.URL.Path != "/api/v1/autoatendimento/escalonamentos" || string(b) != `{"motivo":"reclamacao","resumo":"r"}` {
				t.Errorf("%s %s", r.URL.Path, b)
			}
			envData(w, map[string]any{"equipeNotificada": notificada})
		})
		got, err := s.Escalar(context.Background(), conector.Identidade{Ticket: "tk"}, "reclamacao", "r")
		if err != nil || got != notificada {
			t.Errorf("got=%v err=%v", got, err)
		}
	}
}

func TestBaixarMidia(t *testing.T) {
	raw := []byte("OggS\x00\x01áudio")
	s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.EscapedPath() != "/api/v1/autoatendimento/mensagens/3EB0%2FA/midia" {
			t.Errorf("%s %s", r.Method, r.URL.EscapedPath())
		}
		envData(w, map[string]any{"mime": "audio/ogg; codecs=opus", "tamanho": len(raw), "base64": base64.StdEncoding.EncodeToString(raw)})
	})
	m, err := s.BaixarMidia(context.Background(), conector.Identidade{Ticket: "tk"}, "3EB0/A")
	if err != nil {
		t.Fatal(err)
	}
	if m.Mime != "audio/ogg; codecs=opus" || string(m.Bytes) != string(raw) {
		t.Errorf("midia = %q %q", m.Mime, m.Bytes)
	}
}

func TestErrosTipadosDasOperacoesDoMotor(t *testing.T) {
	cases := []struct {
		nome   string
		status int
		code   string
		is     error
	}{
		{"NOT_FOUND", 404, "NOT_FOUND", ErrNotFound},
		{"MEDIA_TOO_LARGE", 413, "MEDIA_TOO_LARGE", ErrMediaTooLarge},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) { envErro(w, c.status, c.code) })
			_, err := s.BaixarMidia(context.Background(), conector.Identidade{Ticket: "tk"}, "m1")
			var es *ErrSistema
			if !errors.As(err, &es) || es.Code != c.code || es.Status != c.status {
				t.Fatalf("err = %#v", err)
			}
			if !errors.Is(err, c.is) {
				t.Errorf("errors.Is(%v) falhou", c.is)
			}
			if c.is == ErrNotFound && errors.Is(err, ErrMediaTooLarge) {
				t.Error("sentinelas confundidas")
			}
		})
	}
}

func TestErroDeRegraEmOutraOperacaoEAbortar(t *testing.T) {
	s, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) { envErro(w, 422, "VALIDATION_ERROR") })
	_, err := s.Responder(context.Background(), conector.Identidade{Ticket: "tk"}, "x")
	var es *ErrSistema
	if !errors.As(err, &es) || es.Code != "VALIDATION_ERROR" {
		t.Errorf("err = %v", err)
	}

	s2, _ := novoSistema(t, func(w http.ResponseWriter, r *http.Request) { envErro(w, 401, "TICKET_EXPIRED") })
	_, err = s2.Contexto(context.Background(), conector.Identidade{Ticket: "tk"})
	var ab *conector.ErrAbortar
	if !errors.As(err, &ab) || ab.Code != "TICKET_EXPIRED" {
		t.Errorf("err = %v", err)
	}
}

func TestNovoComOperacaoInexistente(t *testing.T) {
	bad := ops
	bad.Midia = "naoExiste"
	if _, err := Novo(nil, lerSpec(t), bad); err == nil {
		t.Error("esperava erro")
	}
}
