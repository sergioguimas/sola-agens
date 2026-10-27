# Sola Agens — plano de build

Como transformar [CONTRATO.md](CONTRATO.md) + [specs/](specs/) em `bin/sola-agens` rodando na VPS da
Sola com o agente Eliza. Padrão de modelos (o mesmo do Smaug): **Opus planeja, orquestra e revisa;
Sonnet escreve o grosso do código**, uma fase por vez, via subagentes.

---

## 1. Regras do build

1. **Commits na `main`** enquanto o serviço não está em produção (padrão da casa); branch de
   desenvolvimento só depois do primeiro deploy. Um commit por fase: `agens: fase N — <o quê>`.
2. **A sessão principal roda em Opus.** Cada fase é delegada a um subagente **Sonnet** (`Agent` com
   `model: "sonnet"`) com o cabeçalho comum + o prompt da fase (§4). A sessão Opus lê o diff, roda
   `go vet ./... && go test ./...`, aplica o checklist (§5) e só então commita. Correção pequena: a
   própria sessão faz; estrutural: volta ao Sonnet com o parecer.
3. **Uma fase por vez, na ordem.** Não avançar com teste vermelho ou `vet` sujo.
4. **Especificação manda.** Decisão que os docs não cobrem: a sessão Opus decide, anota no
   `docs/HISTORICO-DO-BUILD.md` em "Decisões durante o build" e segue. Se contraria o CONTRATO ou a §9 da specs/05,
   para e pergunta ao Sérgio.
5. **Sem rede nos testes.** Gemini, sistema e transcrição só por interface + fake. A spec real do
   Eliza vai para `testdata/` como golden. O aceite (CHECKLIST-ACEITE) é manual, contra o Eliza de
   desenvolvimento.
6. **Não tocar no Smaug.** Pacotes são copiados (A3), nunca importados; o diff não sai de
   `sola-agens/`.
7. **Ambiente:** Windows (PC do Sérgio) com Go 1.26, `CGO_ENABLED=0`. Testes com `t.TempDir()`.

## 2. Prompt de abertura (colar na sessão Opus)

```
Você está em Projetos/sola-agens. Vamos construir o serviço seguindo, nesta ordem, CONTRATO.md,
specs/01..05 e PLANO-DE-BUILD.md. Leia todos antes de qualquer coisa, e também
../redmilab/servicos/smaug/specs/04-integracoes.md §2 e os pacotes do Smaug listados em specs/05 §2
(é de lá que o código é copiado).

Regras: uma fase por vez conforme PLANO-DE-BUILD §3; cada fase é delegada a um subagente Sonnet
(Agent, model "sonnet") com o cabeçalho comum e o prompt da fase (§4); você (Opus) revisa com o
checklist de §5, roda go vet e go test, corrige o que for pequeno, devolve ao Sonnet o que for
estrutural, e commita "agens: fase N — ..." na main. Não avance com teste vermelho. Decisões fora do
spec vão em docs/HISTORICO-DO-BUILD.md → "Decisões durante o build". Ao fim de cada fase, me diga em 3 linhas o que
entrou e o que ficou.

Comece pela fase 1.
```

## 3. Fases da F1 (agente Eliza)

| # | Entrega | Pacotes | Prova |
|---|---|---|---|
| 1 | Esqueleto que sobe e aceita mensagem assinada | `go.mod`, `main.go`, `config`, `agentes`, `entrada` (encaminhado), `httpapi` (`/health`, `/a/{agente}/v1/mensagens` até o 202 com handler no-op), slog, encerramento, `.env.example`, `agentes/eliza.yaml` | `go run .` sobe; `curl /health`; testes de config, registro, assinatura e códigos HTTP |
| 2 | Base | `store` (interface + tipos), `store/sqlite` (migrations embed, SQL portável), `store/storetest`, `trava/memoria`, `manutencao`, rota admin de apagamento, dedupe ligado no `httpapi` | bateria `storetest` verde no SQLite; catch-up da manutenção com relógio falso |
| 3 | OpenAPI, conector e clientes | `openapi` (golden do Eliza), `conector`, `sistema`, `llm` copiado do Smaug + `preco.go` | golden spec → ferramentas; conector e sistema com `httptest`; golden do request Gemini |
| 4 | Agente | `agent` (prompt, loop, dicionário, fastpath, `chamar_humano`), `debounce`, `worker` copiado, ligação `httpapi → debounce → worker → agent → sistema`; pausa e eco | testes do `agent` e ponta a ponta com fakes (specs/05 §5) |
| 5 | Áudio | `transcricao` copiado, `agent/audio.go`, normalização de `conteudo.tipo=audio`, `sistema.BaixarMidia` | ponta a ponta com fakes: áudio curto → transcrição fake → turno normal; áudio longo → resposta fixa sem download |
| 6 | Fechamento | `README.md` + `docs/OPERACAO.md` + `docs/HISTORICO-DO-BUILD.md`, `Dockerfile` final, `avaliacao/` (bateria em YAML de modelo e prompt, Eliza simulado + Gemini real), `health` do container, `go test -race`, deploy em dev, aceite | CHECKLIST-ACEITE preenchido |

### 3.1 Trilha paralela: PR do Eliza

As mudanças de [specs/04 §6](specs/04-integracoes.md#6-o-que-o-eliza-precisa-mudar) são um trabalho
**no repo do Eliza**, contratado em `Eliza/docs/contratos/autoatendimento/07-ajustes-sola-agens.md`
(esteira de contratos do Eliza: contrato Opus → código Sonnet → revisão Opus). Pode correr junto das fases 1–5 do
agens. Dependências:

| Item do Eliza | Bloqueia |
|---|---|
| 1 `x-atendente`, 2 `verificarNomeCadastrado`, 3 motivos novos | aceite (fase 6). A fase 3 usa a spec atual do Eliza com as marcações aplicadas à mão no `testdata/` e é regravada quando o PR entrar |
| 4 áudio | aceite dos itens de áudio; a fase 5 é construída contra fakes |
| 5 campo `atendente` | aceite de pausa configurada e respostas personalizadas; sem ele o agens usa os padrões do motor (já funciona) |

## 4. Prompts por fase (para o subagente Sonnet)

Cabeçalho comum (a sessão Opus prefixa):

```
Você está implementando o serviço Go em Projetos/sola-agens (commit na main). Contrato: CONTRATO.md.
Specs: specs/01-dominio-e-schema.md, 02-ferramentas-e-prompt.md, 03-fluxos.md, 04-integracoes.md,
05-arquitetura-go.md. Leia os que a fase cita antes de escrever. Código a copiar do Smaug fica em
../redmilab/servicos/smaug/internal/ — copie, troque o import path para sola-agens/internal/... e
remova o que é do livro-caixa; NUNCA edite nada fora de sola-agens/. Padrões da casa: config por
lookup (internal/config do Smaug), comentários curtos em português, sem frameworks, puro Go
(CGO_ENABLED=0), Go 1.26, testes table-driven junto com o código, sem rede nos testes. Não invente
campos que os specs não têm; se faltar algo, escolha o mais simples, marque com `// DECISÃO:` e liste
no final. Nunca logue texto de mensagem, ticket, token ou argumento de ferramenta (specs/05 §8).
Termine com `go vet ./... && go test ./...` verdes e um resumo de 5 linhas: arquivos criados,
decisões, o que ficou de fora.
```

**Fase 1**
```
Fase 1 — esqueleto. Leia specs/05 §1, §3, §6, §7, §8; specs/04 §1; specs/03 F1 passos 1–9 e F6;
specs/01 §1, §2, §5.
Crie: go.mod (module sola-agens, go 1.26; deps: godotenv, yaml.v3, uuid), main.go (wiring parcial),
internal/config (todas as variáveis de specs/05 §7 com defaults do .env.example; testes de defaults,
obrigatórias e inválidas), internal/agentes (carrega agentes/*.yaml, valida conforme specs/05 §6 —
exceto "operações existem na spec", que é da fase 3; testes), internal/entrada/encaminhado.go
(struct MensagemEncaminhada espelhando o Zod de specs/04 §1, VerificarAssinatura com HMAC e
tolerância de 300 s em tempo constante, Normalizar → Mensagem de specs/01 §5 com texto, nao_suportado
e audio), internal/httpapi (/health; POST /a/{agente}/v1/mensagens com os passos 1–5, 7–9 do F1 e um
Handler interface no-op; 202 {"aceito":true}). slog JSON; encerramento com signal.NotifyContext.
agentes/eliza.yaml exatamente como specs/05 §6. .env.example completo. Testes de assinatura: válida,
1 byte do corpo trocado, timestamp a 301 s, header ausente; códigos 401/400/404/413/202.
```

**Fase 2**
```
Fase 2 — base. Leia specs/01 inteiro, specs/03 F9 e F10, specs/05 §4–§5.
internal/store: interface Store e tipos de specs/01 §11 (ConversaID{Agente,Tenant,Contato},
Conversa, MensagemMemoria, Turno). internal/store/sqlite: Open com PRAGMAs (WAL, foreign_keys,
busy_timeout 5000), migrations embutidas com schema_version, 0001_init.sql exatamente como specs/01
§10 (SQL portável — nada de strftime, AUTOINCREMENT ou upsert exclusivo), Clock injetável, ids uuid
v7. internal/store/storetest: Rodar(t, nova func() Store) cobrindo toda a interface, incluindo
isolamento de tenant e retenção; o teste do sqlite chama Rodar. internal/trava (interface +
memoria/). internal/manutencao: job diário de specs/03 F9 com catch-up de 3 dias e (nome, data_ref)
marcado só após sucesso (padrão do scheduler do Smaug). httpapi: dedupe (passo 6 do F1: duplicado
responde 202) e DELETE /admin/agentes/{a}/tenants/{t}/contatos/{c} de specs/03 F10.
```

**Fase 3**
```
Fase 3 — OpenAPI, conector e clientes. Leia specs/02 §1–§4, specs/04 §2–§3, specs/05 §2, §3, §5 e
../redmilab/servicos/smaug/specs/04-integracoes.md §2.
internal/llm: copie llm/ do Smaug (provider.go, gemini/, fake.go) sem mudar comportamento; acrescente
preco.go (tabela por modelo, sobrescrita por AGENS_PRECO_<MODELO>, função Custo(modelo, in, out) em
micro-US$) e garanta que GenerateResponse expõe os tokens. internal/openapi: Converter(spec []byte,
ag agentes.Agente) ([]Ferramenta, error) puro, regras de specs/02 §1 (seleção, conversão, $ref, 3.1
nullable, descarte de palavras-chave, colisão), Ferramenta = llm.ToolDecl + mapa de argumentos +
método + caminho; Carregar(ctx, http, ag) com as 5 tentativas do boot; validação das operações do
motor (existem; as quatro do motor sem x-atendente). testdata/eliza.openapi.json = spec atual do Eliza
(Eliza/web/lib/openapi/autoatendimento.ts serializada; peça ao Opus) com x-atendente aplicado à mão
nas operações de specs/02 §3, e testdata/eliza.tools.golden.json. internal/conector: Executor com
specs/02 §2 (montagem, injeção, descarte de argumento inventado com aviso, envelope, códigos de §2.1,
timeout, retentativa só em GET). internal/sistema: Contexto, Responder, Escalar, BaixarMidia tipados
para o Eliza (structs espelhando os Zod de contexto.ts e mensagens.ts, incluindo o campo atendente de
specs/04 §6 item 5 como opcional). Testes de specs/05 §5 para openapi, conector e sistema.
```

**Fase 4**
```
Fase 4 — agente. Leia specs/02 inteiro, specs/03 F1–F4 e F8, specs/01 §3, §4, §6–§8, specs/05 §2–§5.
internal/agent: dicionario.go (padrões de specs/02 §5 e mescla com a config do sistema campo a
campo), prompt.go (template de specs/02 §7, com as variações de IDENTIFICAÇÃO), fastpath.go
(specs/02 §6), run.go adaptado do Smaug (loop com ferramentas do agente + chamar_humano, resposta
vazia, max_iter, erro de regra continua, erro técnico aborta → F2, tokens e custo do turno), pausa e
eco (F3), registro de turno. internal/debounce (specs/03 F8). internal/worker copiado do Smaug com a
chave de specs/05 §2 e Trava por turno. main.go: ligação completa. Testes de specs/05 §5 para agent,
debounce e o ponta a ponta com fakes.
```

**Fase 5**
```
Fase 5 — áudio. Leia specs/02 §8, specs/03 F5, specs/04 §4 e §6 item 4, specs/01 §5.
internal/transcricao: copie do Smaug (interface, gemini, groq, fake); padrão AGENS_TRANSCRICAO=gemini.
internal/agent/audio.go: política por tipo (resposta_fixa | processar), limite audio_max_s antes de
qualquer download, sistema.BaixarMidia com o ticket da própria mensagem, limite de 16 MB,
transcrição, texto no turno e "[áudio Ns] <texto>" na memória; bytes descartados. Normalização de
conteudo.tipo=audio em entrada (forma de specs/04 §6 item 4). Nenhum log com transcrição. Testes:
áudio curto → turno normal com o texto; longo → resposta fixa sem chamar BaixarMidia; 404 da mídia;
falha de transcrição → falhaTecnica sem escalar; imagem → midiaNaoSuportada.
```

**Fase 6**
```
Fase 6 — fechamento. Leia CONTRATO §7–§8, CHECKLIST-ACEITE.md, specs/05 §5.
README.md: configurar (.env, agentes/), subir local e em container, ligar ao Eliza (ATENDENTE_URL =
http://sola-agens:4100/a/eliza, segredos dos dois lados), ver logs, rodar testes, consultas SQL de
custo por agente/tenant/dia, e "Decisões durante o build" consolidando os // DECISÃO:. Dockerfile
multi-stage (static, não-root, volume /data). avaliacao/: a bateria do CHECKLIST em YAML (mensagens
do cliente + verificação esperada) e um runner (go run ./avaliacao) que assina e envia ao agens local
contra o Eliza de dev e imprime o resultado de cada cenário. go test -race ./... limpo.
```

## 5. Checklist de revisão (sessão Opus, por fase)

- [ ] O diff só toca `sola-agens/`. Nada no Smaug nem no Eliza.
- [ ] Nenhuma dependência fora de specs/05 §1 sem `// DECISÃO:` justificando.
- [ ] Nenhum identificador de cliente (tenant, contato, ticket) sai de argumento do LLM; tudo vem da
      entrada autenticada.
- [ ] Ferramenta só existe se a operação tem `x-atendente: true`; operações do motor nunca viram
      ferramenta.
- [ ] Nenhuma escrita é retentada automaticamente (grep pelo caminho de retry no conector).
- [ ] Toda função do `Store` recebe `agente + tenant` (ou a conversa); `storetest` cobre isolamento.
- [ ] SQL da migration roda sem mudança em Postgres (sem `strftime`, `AUTOINCREMENT`, `DEFAULT`
      de data, upsert exclusivo).
- [ ] Nenhum `slog` com texto de mensagem, transcrição, ticket, token ou argumento (grep por `Texto`,
      `texto`, `Ticket`, `args` nas chamadas de log).
- [ ] `/a/{agente}/v1/mensagens` responde antes do LLM; duplicado = 202; fila cheia = 503 com evento
      desmarcado.
- [ ] Loop: chamadas paralelas tratadas; `max_iter`; resposta vazia tratada; erro de regra volta ao
      LLM, erro técnico aborta e escala.
- [ ] Datas e "agora" passam pelo `Clock` injetado e por `AGENS_TZ`.
- [ ] Testes table-driven, sem rede, `t.TempDir()`; `go vet` limpo.

## 6. Depois do build

1. Deploy em container na VPS da Sola, rede do Eliza, sem rota pública (A7). `.env` real com a chave
   do Gemini pago. Passo a passo (build, volume `/data`, serviço do compose, rede): docs/OPERACAO, seções
   [Container](docs/OPERACAO.md#container) e [Configurar](docs/OPERACAO.md#configurar).
2. No Eliza: `ATENDENTE_URL` e `ATENDENTE_ENCAMINHAMENTO_SECRET`; `autoatendimento_config` ativa só
   para a org de teste (`admin`). Valores e ordem: docs/OPERACAO, [Ligar ao Eliza](docs/OPERACAO.md#ligar-ao-eliza).
3. Antes do aceite: `go run ./avaliacao` (modelo e prompt; README, [Avaliação](README.md#avaliação-do-modelo-e-do-prompt)).
   Aceite manual (CHECKLIST-ACEITE) e medições (custo, latência, RAM; consultas em
   [Custos](docs/OPERACAO.md#custos)). Anotar em docs/OPERACAO.md.
4. Só depois: ligar para uma org real, acompanhando o log de turnos na primeira semana.
5. Backlog (CONTRATO §9) e o módulo de treinamento (§11, rediscutir) vão para `ROADMAP.md`.
