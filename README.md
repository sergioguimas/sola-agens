# Sola Agens — atendente da Sola

**Status:** em construção (09/10/2026). Todas as
decisões da F1 fechadas em 09/10 ([CONTRATO §10](CONTRATO.md#10-decisões-da-f1)). Fase 1 do build
feita (esqueleto). Próximo passo: fase 2 (base). PR do Eliza codado (contrato 07, aceite pendente).

Plataforma interna da Sola para pôr um atendente (LLM com ferramentas) em qualquer sistema da Sola. O
motor lê a **OpenAPI** do sistema e transforma as operações liberadas em ferramentas. Primeiro agente:
**Eliza** (F1). Depois: site da Sola, Axios Calc, SolaBridge.

*Agens*, do latim: "o que age".

| Doc | Para quê |
|---|---|
| [docs/DECISOES.md](docs/DECISOES.md) | por que existe e as decisões de origem (P1–P6) |
| [CONTRATO.md](CONTRATO.md) | **comece aqui** — o que o serviço faz, segurança, decisões abertas |
| [specs/](specs/) | domínio e schema · ferramentas e prompt · fluxos · integrações · arquitetura Go |
| [PLANO-DE-BUILD.md](PLANO-DE-BUILD.md) | fases, prompts por fase (Sonnet), checklist de revisão (Opus) |
| [CHECKLIST-ACEITE.md](CHECKLIST-ACEITE.md) | bateria de conversas e medições |
| [.env.example](.env.example) | variáveis |

Estrutura, padrão de documentação e boa parte do código vêm do Smaug
(`redmilab/servicos/smaug`) — ver [specs/05 §2](specs/05-arquitetura-go.md#2-mapa-de-reaproveitamento-do-smaug).

## Decisões durante o build

**Fase 1 (09/10/2026, commit c69da42)**

- Tipos `Mensagem`/`Anexo` em `internal/mensagem` (sem ciclo entre `entrada` e `agent`); `Anexo.TipoOriginal` guarda o tipo do canal quando vira `outro`.
- `AGENS_LISTEN` aceita `0.0.0.0` (diferente do Smaug): o agens roda em container, e a exposição é controlada pela rede Docker (A7).
- `Agente` guarda segredo, URL base e token em campos não exportados; `String()`/`LogValue()` mostram só id e modo.
- Registro lido com `KnownFields(true)`: campo desconhecido no yaml é erro. `operacoes.confirmar`/`cancelar` opcionais (só o fastpath usa); as quatro do motor obrigatórias. Limite `0` = padrão; negativo ou `rajada_max_ms < rajada_ms` = erro.
- `AGENS_PRECO_*` lido como mapa bruto em `Config.Precos`; o parse fica para a fase 3 (`llm/preco.go`).
- Erro do handler que não seja `ErrFilaCheia` → 500. Dedupe entra pela `Server.Dedupe` (fase 2).
- Teste de fumaça: binário real, `/health` ok, POST assinado → 202, assinatura adulterada → 401, log sem telefone/texto/segredo.
