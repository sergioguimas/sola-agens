// Comando avaliacao: bateria de avaliação de MODELO e PROMPT do sola-agens.
//
// Roda os cenários de avaliacao/cenarios.yaml contra um Eliza SIMULADO em
// processo e o Gemini REAL (GEMINI_API_KEY). Não conversa com o Eliza de
// verdade nem manda WhatsApp. Rode antes do aceite manual e a cada troca de
// modelo ou de prompt: go run ./avaliacao
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/joho/godotenv"

	"sola-agens/internal/config"
	"sola-agens/internal/llm"
	"sola-agens/internal/llm/gemini"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := executar(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func executar(ctx context.Context, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("avaliacao", flag.ContinueOnError)
	fs.SetOutput(errw)
	modelo := fs.String("modelo", "", "modelo do Gemini (padrão: AGENS_MODEL)")
	so := fs.Int("cenario", 0, "roda só o cenário com este id")
	vezes := fs.Int("vezes", 1, "repete cada cenário N vezes (estabilidade)")
	arquivo := fs.String("arquivo", "", "yaml de cenários (padrão: o embutido)")
	spec := fs.String("spec", "", "eliza.openapi.json (padrão: internal/openapi/testdata)")
	agentes := fs.String("agentes", "", "diretório agentes/ (padrão: ./agentes)")
	verboso := fs.Bool("v", false, "mostra as respostas enviadas ao cliente (texto de teste)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *vezes < 1 {
		fmt.Fprintln(errw, "-vezes deve ser >= 1")
		return 2
	}

	// .env é opcional; as variáveis do ambiente valem de qualquer jeito.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(errw, "erro lendo .env:", err)
		return 2
	}
	chave := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if chave == "" {
		fmt.Fprintln(errw, "GEMINI_API_KEY não definida: a avaliação chama o Gemini de verdade (plano pago).")
		fmt.Fprintln(errw, "Defina no ambiente ou no .env e rode de novo: go run ./avaliacao")
		return 2
	}
	cfg, err := config.LoadConfigFrom(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(errw, "configuração:", err)
		return 2
	}
	if err := llm.ConfigurarPrecos(config.PrecosBrutos(os.Environ())); err != nil {
		fmt.Fprintln(errw, "preços:", err)
		return 2
	}
	if *modelo == "" {
		*modelo = cfg.Model
	}

	cenarios, err := CarregarArquivo(*arquivo)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	if *so != 0 {
		var um []Cenario
		for _, c := range cenarios {
			if c.ID == *so {
				um = append(um, c)
			}
		}
		if len(um) == 0 {
			fmt.Fprintf(errw, "cenário %d não existe\n", *so)
			return 2
		}
		cenarios = um
	}

	specPath, err := achar(*spec, "internal/openapi/testdata/eliza.openapi.json", "../internal/openapi/testdata/eliza.openapi.json")
	if err != nil {
		fmt.Fprintln(errw, "spec do Eliza:", err)
		return 2
	}
	agDir, err := achar(*agentes, "agentes", "../agentes")
	if err != nil {
		fmt.Fprintln(errw, "diretório agentes/:", err)
		return 2
	}

	log := slog.New(slog.NewTextHandler(errw, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b, err := Preparar(ctx, Opcoes{
		Provedor: gemini.NewClient(chave, nil, log), Modelo: *modelo,
		Temperature: cfg.Temperature, ThinkingBudget: cfg.ThinkingBudget,
		SpecPath: specPath, AgentesDir: agDir, Log: log,
	})
	if err != nil {
		fmt.Fprintln(errw, "preparar:", err)
		return 2
	}
	defer b.Fechar()

	fmt.Fprintf(out, "modelo %s · %d cenário(s) × %d · temperatura %g\n\n", *modelo, len(cenarios), *vezes, cfg.Temperature)
	var todos []Resultado
	for _, c := range cenarios {
		for rep := 1; rep <= *vezes; rep++ {
			if ctx.Err() != nil {
				fmt.Fprintln(errw, "interrompido")
				return 1
			}
			todos = append(todos, b.Rodar(ctx, c, rep))
		}
	}
	if Relatorio(out, todos, *vezes, *verboso) {
		return 0
	}
	return 1
}

// achar devolve o caminho dado ou o primeiro candidato que existe.
func achar(dado string, candidatos ...string) (string, error) {
	if dado != "" {
		if _, err := os.Stat(dado); err != nil {
			return "", err
		}
		return dado, nil
	}
	for _, c := range candidatos {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("não achei %s (rode da raiz do repositório ou passe a flag)", candidatos[0])
}

// Relatorio imprime a tabela e os totais; devolve true se tudo passou.
func Relatorio(w io.Writer, rs []Resultado, vezes int, verboso bool) bool {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tCENÁRIO\tRESULTADO\tFERRAMENTAS\tTOKENS (ent/saí)\tUS$\tLATÊNCIA")
	var custo int64
	var lat time.Duration
	passou := 0
	for _, r := range rs {
		status := "passou"
		if !r.Passou() {
			status = "FALHOU"
		} else {
			passou++
		}
		id := fmt.Sprint(r.Cenario.ID)
		if vezes > 1 {
			id += fmt.Sprintf(" (%d/%d)", r.Rep, vezes)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d/%d\t%.5f\t%s\n", id, corta(r.Cenario.Nome, 46), status,
			ferramentas(r.Ferramentas), r.TokensIn, r.TokensOut, float64(r.CustoMicro)/1e6, r.Latencia.Round(10*time.Millisecond))
		custo += r.CustoMicro
		lat += r.Latencia
	}
	tw.Flush()

	for _, r := range rs {
		for _, f := range r.Falhas {
			fmt.Fprintf(w, "\n  falha #%d (%d/%d): %s", r.Cenario.ID, r.Rep, vezes, f)
		}
	}
	if verboso {
		for _, r := range rs {
			for _, t := range r.Respostas {
				fmt.Fprintf(w, "\n  resposta #%d (%d/%d) %s", r.Cenario.ID, r.Rep, vezes, t)
			}
		}
	}
	if vezes > 1 {
		fmt.Fprint(w, "\n\nestabilidade:")
		visto := map[int]bool{}
		for _, r := range rs {
			if visto[r.Cenario.ID] {
				continue
			}
			visto[r.Cenario.ID] = true
			ok := 0
			for _, x := range rs {
				if x.Cenario.ID == r.Cenario.ID && x.Passou() {
					ok++
				}
			}
			fmt.Fprintf(w, " #%d %d/%d;", r.Cenario.ID, ok, vezes)
		}
	}
	n := len(rs)
	fmt.Fprintf(w, "\n\n%d/%d passaram · custo total US$ %.5f · custo médio por cenário US$ %.5f · latência média %s\n",
		passou, n, float64(custo)/1e6, float64(custo)/1e6/float64(max(n, 1)), (lat / time.Duration(max(n, 1))).Round(10*time.Millisecond))
	return passou == n
}

func ferramentas(l []string) string {
	if len(l) == 0 {
		return "-"
	}
	return corta(strings.Join(l, ","), 60)
}

func corta(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
