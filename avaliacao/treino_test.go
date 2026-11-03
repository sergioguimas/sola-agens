package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"sola-agens/internal/llm"
	"sola-agens/internal/treino"
)

// treinadorFalso responde como o modelo responderia: conduz ensaios em ordem e,
// quando vê o schema, devolve a extração. Prova que a bateria roda de ponta a
// ponta sem gastar crédito; não diz nada sobre a qualidade do modelo real.
type treinadorFalso struct {
	proximo  int
	pendente string
}

func (f *treinadorFalso) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if req.ResponseSchema != nil {
		return f.extrair(req)
	}
	ultimo := req.Contents[len(req.Contents)-1].Parts[0]
	if ultimo.FunctionResponse != nil {
		r := llm.TextoResp(`"` + f.pendente + `"`)
		return &r, nil
	}
	if strings.Contains(ultimo.Text, "nunca uso 😂") {
		r := llm.GenerateResponse{FinishReason: "STOP", Parts: []llm.Part{
			{FunctionCall: &llm.FunctionCall{Name: "registrar_preferencia", Args: map[string]any{
				"campo": "emojis.evitar", "valor": "😂", "citacao": "nunca uso 😂 com cliente"}}},
			{FunctionCall: &llm.FunctionCall{Name: "registrar_preferencia", Args: map[string]any{
				"campo": "tratamento", "valor": "você", "citacao": "Prefiro tratar por você"}}},
		}}
		f.pendente = "pode continuar?"
		return &r, nil
	}
	cenario := treino.Cenarios[f.proximo%len(treino.Cenarios)]
	f.proximo++
	f.pendente = "fala de " + cenario
	r := llm.CallResp("iniciar_ensaio", map[string]any{"cenario": cenario, "fala_cliente": f.pendente})
	return &r, nil
}

func (f *treinadorFalso) extrair(req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	var entrada struct {
		Pares []struct {
			N        int    `json:"par"`
			Cenario  string `json:"cenario"`
			Resposta string `json:"resposta"`
		} `json:"pares"`
	}
	texto := strings.TrimPrefix(req.Contents[0].Parts[0].Text, "ENTRADA\n")
	if err := json.Unmarshal([]byte(texto), &entrada); err != nil {
		return nil, err
	}
	saida := map[string]any{"observacoes": []map[string]any{}, "instrucoesIgnoradas": []string{}, "paresForaDoRoteiro": []int{}}
	obs := []map[string]any{{"campo": "saudacoes", "valor": "Oii flor", "tipo": "estilo", "citacao": "Oii flor"}}
	var fora []int
	for _, p := range entrada.Pares {
		if p.Resposta == falaDeclaracao || p.Resposta == falaInjecao {
			fora = append(fora, p.N)
			continue
		}
		obs = append(obs, map[string]any{"campo": "exemplos", "cenario": p.Cenario, "valor": p.Resposta, "tipo": "estilo"})
		if strings.Contains(p.Resposta, "R$ 150") {
			obs = append(obs, map[string]any{"campo": "nota", "valor": "A unha sai R$ 150", "tipo": "fato", "citacao": "R$ 150"})
		}
	}
	saida["observacoes"], saida["paresForaDoRoteiro"] = obs, fora
	raw, err := json.Marshal(saida)
	if err != nil {
		return nil, err
	}
	r := llm.TextoResp(string(raw))
	return &r, nil
}

func TestBateriaDoTreinoRodaDePontaAPonta(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ok, err := AvaliarTreino(context.Background(), &treinadorFalso{}, "gemini-3.6-flash", 0, 0, log, &out, false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("a bateria reprovou um treinador correto:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "perfil v") {
		t.Errorf("saída sem o perfil:\n%s", out.String())
	}
}
