# 05 — Arquitetura Go

Um binário, um processo, uma base (A1). Puro Go (`CGO_ENABLED=0`), Go 1.26 — mesma toolchain do
Smaug. Roda em container na VPS da Sola.

---

## 1. Layout

```
sola-agens/
├── main.go                  wiring: config → store → agentes (carrega OpenAPI) → provider → worker → http; sinais
├── go.mod                   module sola-agens
├── .env.example
├── README.md                como operar
├── CONTRATO.md, PLANO-DE-BUILD.md, CHECKLIST-ACEITE.md, specs/, docs/DECISOES.md
├── agentes/                 config por agente (A4): eliza.yaml (persona, sistema, openapi, segredos por env)
└── internal/
    ├── config/              Config + LoadConfigFrom(lookup)                         ← padrão do Smaug
    ├── agentecfg/           carrega e valida agentes/*.yaml                          NOVO
    ├── openapi/             lê a spec, filtra x-atendente, gera []llm.ToolDecl       NOVO
    ├── conector/            executa a ferramenta: monta o HTTP, injeta identidade,   NOVO
    │                        traduz o envelope { data | error } para o LLM
    ├── entrada/             modos de entrada: encaminhado (F1); canal próprio (F2+) NOVO
    ├── store/               SQLite: migrações (embed), memória, eventos (dedupe),   ← memoria.go/eventos.go do Smaug
    │                        log por turno, pausa humana
    ├── llm/                 Provider + gemini/ + fake                                ← copiado do Smaug (A3)
    ├── agent/               loop de ferramentas, prompt base + persona, fastpath,   ← run.go/fastpath.go/audio.go do Smaug, sem domínio
    │                        roteador de áudio
    ├── transcricao/         Groq/Gemini + fake                                       ← copiado do Smaug
    ├── worker/              fila por conversa, sequencial, timeout                   ← copiado do Smaug
    └── httpapi/             /health, /v1/mensagens (encaminhado), validação de assinatura
```

## 2. Mapa de reaproveitamento do Smaug

| Pacote do Smaug | Destino | O que muda |
|---|---|---|
| `internal/llm`, `llm/gemini` | copiado | nada |
| `internal/worker` | copiado | chave da fila = `agente:contato` |
| `internal/transcricao` | copiado | nada |
| `internal/agent/run.go` | adaptado | sem `store` de domínio; ferramentas vêm do `openapi` + `conector`, não de `register()` em `init()` |
| `internal/agent/fastpath.go` | adaptado | os tokens viram config do agente |
| `internal/agent/audio.go` | adaptado | limites por agente |
| `internal/agent/tools.go` (`ToolCtx`, `ToolError`, registro) | adaptado | `ToolError` passa a ser o `error` do envelope do sistema |
| `internal/store/memoria.go`, `eventos.go`, migrações | adaptado | chave por agente |
| `internal/config`, `httpapi`, `main.go` | padrão | conteúdo novo |
| `internal/gateway` | **não na F1** | o modo encaminhado responde pelo sistema; volta no canal próprio |
| `internal/domain`, `agent/tools_*.go`, `export`, `scheduler` | **fica no Smaug** | é o livro-caixa |

## 3. Fluxo de dependências

**A preencher.** Regra herdada: `domain`-like puro não importa I/O; `agent` depende de interfaces
(`llm.Provider`, `conector.Executor`, `store`), nunca de HTTP direto.

## 4. Concorrência

Herdado do Smaug: uma goroutine por conversa, fila de 100, timeout por mensagem. **A preencher:**
debounce da rajada (~3 s) antes de enfileirar.

## 5. Testes

Herdado do Smaug: sem rede nos testes; LLM e sistemas por interface + fake; golden do request ao
Gemini. Novo: golden da conversão OpenAPI → `ToolDecl` usando a spec real do Eliza salva em
`testdata/`.

## 6. Decisões registradas

Ver CONTRATO §10 (A1–A8) quando fecharem.
