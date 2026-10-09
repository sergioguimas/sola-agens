# Sola Agens — atendente da Sola

**Status:** modelo de documentação montado (09/10/2026) a partir do Smaug; **sem código**. Todas as
decisões da F1 fechadas em 09/10 ([CONTRATO §10](CONTRATO.md#10-decisões-da-f1)). Specs, plano de
build e aceite preenchidos. Próximo passo: fase 1 do [PLANO-DE-BUILD](PLANO-DE-BUILD.md) e, em
paralelo, o contrato do PR do Eliza ([specs/04 §6](specs/04-integracoes.md#6-o-que-o-eliza-precisa-mudar)).

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

(vazio)
