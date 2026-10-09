# 05 — Arquitetura Go

Um binário, um processo, uma base (A1, B1). Puro Go (`CGO_ENABLED=0`), Go 1.26 — mesma toolchain do
Smaug. Roda em container na VPS da Sola.

---

## 1. Layout

```
sola-agens/
├── main.go                  wiring (F6 do 03): config → agentes → store → openapi → llm → worker → http; sinais
├── go.mod                   module sola-agens
├── Dockerfile               multi-stage, imagem final distroless/static, usuário não-root, volume /data
├── .env.example
├── README.md                como operar
├── CONTRATO.md, PLANO-DE-BUILD.md, CHECKLIST-ACEITE.md, specs/, docs/DECISOES.md
├── agentes/
│   └── eliza.yaml           registro do agente (§6)
├── avaliacao/               bateria de conversas (CHECKLIST) em YAML + runner contra o Eliza de dev
└── internal/
    ├── config/              Config + LoadConfigFrom(lookup)                              ← padrão do Smaug
    ├── agentes/             carrega e valida agentes/*.yaml → []Agente                       NOVO
    ├── openapi/             baixa a spec, filtra x-atendente, converte → []llm.ToolDecl     NOVO
    │                        + mapa de argumentos (path/query/body); testdata/ com golden
    ├── conector/            Executor: monta HTTP, injeta identidade, traduz envelope         NOVO
    ├── sistema/             operações do motor sobre o conector: Contexto, Responder,       NOVO
    │                        Escalar, BaixarMidia (tipadas para o Eliza na F1)
    ├── entrada/             encaminhado.go (F1): struct, assinatura, normalização → Mensagem NOVO
    ├── store/               interface Store (01 §11) + tipos
    │   ├── sqlite/          implementação; migrations/ (embed) em SQL portável           ← memoria/eventos do Smaug
    │   └── storetest/       bateria de contrato Rodar(t, nova func() Store)                 NOVO
    ├── trava/               interface Trava + memoria/                                       NOVO
    ├── llm/                 Provider, gemini/, fake.go, preco.go                             ← copiado do Smaug
    ├── transcricao/         Transcritor: gemini, groq, fake                                  ← copiado do Smaug
    ├── agent/               Run(turno): prompt, loop, fastpath, dicionário, áudio           ← run.go/fastpath.go do Smaug, sem domínio
    ├── debounce/            janela de rajada por conversa (03 F8)                            NOVO
    ├── worker/              fila por conversa, sequencial, timeout                           ← copiado do Smaug
    ├── manutencao/          job diário de retenção com catch-up (03 F9)                      ← padrão do scheduler do Smaug
    └── httpapi/             /health, /a/{agente}/v1/mensagens, /admin/... (03 F10)
```

Dependências diretas (todas puro Go): `modernc.org/sqlite`, `github.com/joho/godotenv`,
`github.com/google/uuid` (v7), `golang.org/x/text` (normalização), `gopkg.in/yaml.v3` (registro).
Nada de framework HTTP, ORM, SDK do Google nem biblioteca de OpenAPI: o subconjunto que o conversor
usa é pequeno e o golden trava o comportamento.

## 2. Mapa de reaproveitamento do Smaug (A3: molde, evolui separado)

| Pacote do Smaug | Destino | O que muda |
|---|---|---|
| `internal/llm`, `llm/gemini` | copiado | + `preco.go`; tokens somados por turno |
| `internal/worker` | copiado | chave = `agente:tenant:contato`; item = turno da rajada; sem `gateway` (a resposta sai pelo `sistema`) |
| `internal/transcricao` | copiado | padrão `gemini` |
| `internal/agent/run.go` | adaptado | ferramentas do `openapi` + `conector`; fallbacks de domínio do Smaug (`fallbackResumo` etc.) removidos; resposta vazia conforme 03 F1 |
| `internal/agent/fastpath.go` | adaptado | palavras do dicionário combinado; aciona operação do sistema (02 §6) |
| `internal/agent/audio.go` | reescrito | só "baixa → transcreve → texto" (02 §8); sem roteador comando/transcrição |
| `internal/agent/tools.go` | adaptado | registro vira o conjunto de `ToolDecl` do agente + `chamar_humano`; `ToolError` = erro de regra do envelope |
| `internal/store/memoria.go`, `eventos.go` | adaptado | atrás da interface `Store`, chaves da 01 §1, SQL portável |
| `internal/scheduler` | adaptado em `manutencao` | só o job de retenção |
| `internal/config`, `httpapi`, `main.go` | padrão | conteúdo novo |
| `internal/gateway` | **não na F1** | volta quando houver canal próprio (F2 widget, ou WhatsApp direto) |
| `internal/domain`, `agent/tools_*.go`, `export`, `image.go` | **fica no Smaug** | |

Ao copiar, trocar o import path (`smaug/internal/...` → `sola-agens/internal/...`) e apagar o que é
do livro-caixa. Cópia registrada no README ("copiado do Smaug em <commit>") para saber de onde veio.

## 3. Fluxo de dependências

```
httpapi ─▶ entrada ─▶ debounce ─▶ worker ─▶ agent ─┬─▶ llm.Provider
   │                                               ├─▶ conector.Executor ─▶ (HTTP do sistema)
   │                                               ├─▶ sistema (Contexto/Responder/Escalar/Midia, sobre o conector)
   │                                               ├─▶ transcricao.Transcritor
   └─▶ store (eventos, pausa)                      └─▶ store.Store, trava.Trava
```

- `agent` depende só de interfaces (`llm.Provider`, `conector.Executor`, `sistema.Sistema`,
  `transcricao.Transcritor`, `store.Store`, `trava.Trava`) e de um `Clock`.
- `openapi` não faz I/O depois do boot; `Converter(spec []byte, registro) ([]Ferramenta, error)` é
  pura e testada por golden.
- `entrada` não conhece `agent`: devolve `Mensagem` + identidade (`tenant`, `contato`, `ticket`).
- Ninguém importa `store/sqlite` além do `main.go`.

## 4. Concorrência

- **Debounce:** um `time.Timer` por conversa ativa num `map` protegido por mutex; ao disparar,
  entrega um item à fila. Teto de conversas abertas no debounce = `AGENS_MAX_CONVERSAS` (1000);
  acima disso a mensagem vai direto para a fila sem esperar.
- **Worker:** uma goroutine por conversa com fila de 100 (Smaug); a goroutine morre após 10 min sem
  item. Cada turno pega `Trava.Adquirir(chave)` — na F1 sempre livre (um processo), é o ponto de
  troca para réplicas.
- **Timeout do turno:** `limites.turno_timeout_s` (90 s) via `context.WithTimeout`.
- **Ticket mais recente:** guardado no item da fila (vem do debounce); não há estado global de ticket.
- **Recarga (`SIGHUP`):** troca atômica (`atomic.Pointer`) do conjunto de agentes/ferramentas; turno
  em andamento termina com o conjunto antigo.

## 5. Testes

Sem rede; LLM, sistema e transcrição por interface + fake. Obrigatórios:

| Pacote | Testes |
|---|---|
| `config` | defaults, obrigatórias, valores inválidos |
| `agentes` | yaml válido; cada campo obrigatório ausente; env de segredo não definida → erro |
| `openapi` | golden Eliza (spec → ferramentas); `x-atendente` filtra; operação do motor marcada → erro; colisão de nome → erro; `$ref`; `type` com `null` → `nullable` |
| `conector` | `httptest`: path/query/body montados; headers injetados; argumento inventado descartado; 422 → erro de regra; `TICKET_EXPIRED` → aborta; GET retenta 1x, POST não |
| `entrada` | assinatura válida/inválida/1 byte trocado; timestamp fora da janela; normalização texto/áudio/nao_suportado |
| `store/storetest` | toda a interface; tenants isolados (mesmo contato em dois tenants não se vê); retenção; apagamento |
| `debounce` | 3 mensagens em 2 s = 1 item; teto de 8 s; ticket mais recente |
| `agent` | loop com fake (tool → resposta); resposta vazia → nova chamada → fallback; `max_iter`; erro de regra continua; erro técnico aborta e escala; fastpath 1/0/2 aguardando; pausado não chama LLM; prompt golden (identificado, desconhecido com e sem nomeExibicao, ambíguo) |
| `httpapi` | 401/400/404/413/202; duplicado = 202; fila cheia = 503 e evento desmarcado; admin sem token = 401 |
| ponta a ponta | fakes: POST assinado → contexto fake → LLM fake chama `criarAgendamento` → `httptest` do sistema recebeu o POST com o ticket → `POST /mensagens` recebeu a resposta → eco registrado → `deMim` com o mesmo id não pausa; com outro id pausa |

`go vet ./...` limpo; `go test -race ./...` na fase de fechamento.

## 6. Registro do agente

`agentes/eliza.yaml`:

```yaml
id: eliza
modo_identidade: encaminhado
entrada:
  segredo_env: ELIZA_ENCAMINHAMENTO_SECRET
  header_assinatura: X-Eliza-Assinatura
  header_timestamp: X-Eliza-Timestamp
sistema:
  base_url_env: ELIZA_BASE_URL
  openapi_path: /api/v1/autoatendimento/openapi.json
  token_env: ELIZA_AUTOATENDIMENTO_API_TOKEN
  header_ticket: X-Autoatendimento-Ticket
  headers_fixos:
    X-Autoatendimento-Versao: "1"
operacoes:            # operationId; as quatro primeiras não podem ter x-atendente
  contexto: obterContexto
  responder: enviarMensagem
  escalar: escalarParaEquipe
  midia: baixarMidia
  confirmar: confirmarAgendamento   # fastpath
  cancelar: cancelarAgendamento     # fastpath
midia:
  audio: processar
  imagem: resposta_fixa
  documento: resposta_fixa
  video: resposta_fixa
  figurinha: resposta_fixa
  outro: resposta_fixa
limites:
  max_iter: 8
  rajada_ms: 3000
  rajada_max_ms: 8000
  memoria_mensagens: 20
  memoria_horas: 24
  audio_max_s: 120
  turno_timeout_s: 90
# modelo: gemini-2.5-flash   # opcional; padrão AGENS_MODEL
```

Validação no boot: campos obrigatórios; envs referenciadas existem e não estão vazias; `id` único e
`^[a-z][a-z0-9-]{1,31}$`; operações do motor existem na spec depois do download.

## 7. Configuração (env)

Ver `.env.example`. Motor: `AGENS_LISTEN`, `AGENS_DATA_DIR`, `AGENS_TZ`, `AGENS_LOG_DEBUG`,
`AGENS_AGENTES_DIR` (`./agentes`), `GEMINI_API_KEY`, `AGENS_MODEL`, `AGENS_TEMPERATURE`,
`AGENS_THINKING_BUDGET`, `AGENS_SISTEMA_TIMEOUT`, `AGENS_PAUSA_HUMANA_HORAS`, `AGENS_RETENCAO_DIAS`,
`AGENS_MANUTENCAO_HORA`, `AGENS_ENCERRAMENTO_S`, `AGENS_MAX_CONVERSAS`, `AGENS_ADMIN_TOKEN`,
`AGENS_TRANSCRICAO`, `GROQ_API_KEY`, `AGENS_PRECO_*`. Agente Eliza: `ELIZA_BASE_URL`,
`ELIZA_ENCAMINHAMENTO_SECRET`, `ELIZA_AUTOATENDIMENTO_API_TOKEN`.

## 8. Log

`slog` JSON em stdout. Campos fixos: `agente`, `tenant`, `conversa` (= `sha256(contato)[:12]`, nunca
o telefone), `turno`. Nunca: texto de mensagem, transcrição, argumento ou resultado de ferramenta,
ticket, token. `AGENS_LOG_DEBUG=true` libera argumentos e resultados — pontual, como no Smaug.

## 9. Decisões registradas

CONTRATO §10 (A1–A8, M1–M4, B1–B4). Decisões tomadas ao escrever estas specs, para revisão:

| # | Decisão | Onde |
|---|---|---|
| S1 | Rota de entrada `/a/{agente}/v1/mensagens`: o caminho escolhe o segredo; o Eliza não muda (`ATENDENTE_URL` aponta para `/a/eliza`) | 04 §1 |
| S2 | Duplicada responde **202**, não 204: qualquer outro código faz o Eliza cair no fluxo de palavra-chave | 03 F1 |
| S3 | Fastpath compara o texto **inteiro**; "não", "remarcar", "outro dia" saem da lista de cancelar e vão ao LLM | 02 §5 |
| S4 | Áudio = transcrever e tratar como texto; sem turno multimodal e sem o modo transcrição do Smaug | 02 §8 |
| S5 | Transcrição padrão `gemini` (mesmo operador já aceito), não `groq` | 04 §4 |
| S6 | Conversa pausada não responde nem ao fastpath ("sim" ao lembrete fica para a equipe ou para depois da pausa) | 01 §4 |
| S7 | Ticket nunca vai para a base | 04 §1 |
| S8 | `chamar_humano` é ferramenta do motor, não `x-atendente`: o motor também escala sozinho e o limite estourado não derruba o turno | 02 §4, 03 F4 |
