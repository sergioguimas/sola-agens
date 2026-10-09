# Sola Agens — contrato do serviço

**Status:** modelo montado em 09/10/2026 a partir do Smaug (`redmilab/servicos/smaug`). Decisões de
origem fechadas ([docs/DECISOES.md](docs/DECISOES.md)); **decisões abertas da F1 na §10** — o
contrato só fecha depois delas.
**Papel:** plataforma interna da Sola para pôr um atendente (LLM com ferramentas) em qualquer
sistema da Sola. O motor lê a **OpenAPI** do sistema, transforma as operações liberadas em
ferramentas e conversa com o cliente final pelo canal (WhatsApp; widget web na F2).
**Origem do código:** motor do Smaug (LLM, loop de ferramentas, fila por chat, fastpath, áudio,
imagem). O que é financeiro fica no Smaug.

Documentos que detalham este contrato:

| Doc | Conteúdo |
|---|---|
| [specs/01-dominio-e-schema.md](specs/01-dominio-e-schema.md) | agente, conversa, memória, dedupe, log por turno, retenção, schema |
| [specs/02-ferramentas-e-prompt.md](specs/02-ferramentas-e-prompt.md) | conector OpenAPI → ferramentas, prompt base + persona do agente, fastpath |
| [specs/03-fluxos.md](specs/03-fluxos.md) | passo a passo de cada fluxo, incluindo falhas |
| [specs/04-integracoes.md](specs/04-integracoes.md) | entrada encaminhada (Eliza), gateway WhatsApp, Gemini, chamadas aos sistemas |
| [specs/05-arquitetura-go.md](specs/05-arquitetura-go.md) | pacotes, o que vem do Smaug, concorrência, testes |
| [PLANO-DE-BUILD.md](PLANO-DE-BUILD.md) | fases, prompts por fase, revisão |

---

## 1. Fronteiras

```
 Eliza ──POST /v1/mensagens (assinado)──▶ sola-agens  (agente "eliza", modo encaminhado)
                                            │ GET  {sistema}/openapi.json        (ferramentas)
                                            │ POST Gemini generateContent       (function calling)
                                            │ base própria                       (memória, dedupe, log, config)
                                            ├─ {sistema} operações x-atendente   (com o ticket do turno)
                                            └─ {sistema} POST /mensagens         (resposta ao cliente, modo encaminhado)
```

- **Entrada (F1):** mensagem encaminhada pelo sistema, assinada (contrato do Eliza:
  `Eliza/docs/contratos/autoatendimento/06-mensagens-e-encaminhamento.md`). Canal próprio
  (WhatsApp direto, widget web) entra nas fases seguintes.
- **Saída:** no modo encaminhado, a resposta volta **pelo sistema** (o agens não tem a chave do
  WhatsApp do tenant).
- **Não faz:** não acessa banco de nenhum sistema; não executa operação que a OpenAPI não marque como
  liberada; não decide identidade do cliente (vem do ticket/asserção); não guarda mídia.
- **Onde roda o pesado:** a LLM e a transcrição são externas. No servidor: HTTP, fila e a base
  própria. RAM alvo < 50 MB.

## 2. Gatilho — o que o agens processa

| Regra | Valor | Por quê |
|---|---|---|
| Origem | `POST /v1/mensagens` com `X-Eliza-Assinatura` válida e `X-Eliza-Timestamp` recente (§5) | só o sistema dono do agente encaminha |
| Agente | resolvido pela origem assinada (segredo por agente), nunca pelo corpo | um sistema não fala em nome de outro |
| `deMim` | não vai ao agente; **pausa** a conversa (intervenção humana) | o dono respondeu pelo celular |
| Tipo | texto → agente; áudio → roteador (§3, herdado do Smaug); imagem → **a decidir**; `nao_suportado` → resposta fixa | |
| Idempotência | `mensagemId` já processado → `202` sem reprocessar | o sistema não retenta, mas a Evolution sim |
| Rajada | mensagens do mesmo contato em ~3 s viram um turno só | "oi" / "queria marcar" / "amanhã" |
| Ordem | uma conversa por vez, na ordem de chegada (fila por chat do Smaug) | |

## 3. Comportamento (v1 = F1, agente Eliza)

O agente faz o que a OpenAPI do sistema libera. Para o Eliza, na F1: consultar contexto, serviços,
profissionais e horários; listar, criar, remarcar, cancelar e confirmar os próprios agendamentos; ler
e manter o próprio cadastro; chamar um humano. Regras de negócio são do sistema — o agens só repassa
a mensagem de erro do sistema ao cliente, em linguagem natural.

**A preencher na F1:** lista final de ferramentas, comportamento com cliente `desconhecido` e
`ambiguo`, fastpath (quais respostas não passam pelo LLM).

## 4. Resposta — como o texto volta

Herdado do Smaug: texto curto para WhatsApp, `*negrito*` como único markdown, mensagem longa dividida.
Falha do LLM ou do sistema → resposta fixa ("não consegui agora, um atendente vai te responder") e
escalonamento. **A preencher:** textos fixos.

## 5. Segurança e privacidade

- **Assinatura de entrada:** `hex(HMAC-SHA256(segredo_do_agente, timestamp + "." + corpo))`,
  `timingSafeEqual`, timestamp com no máximo 5 min de diferença.
- **Identidade do cliente:** nunca vem do LLM. No modo encaminhado, o ticket que chega com a mensagem
  é repassado ao sistema em toda chamada de ferramenta daquele turno; o sistema valida.
- **Credenciais:** só o token de serviço de cada sistema e a chave do Gemini, por env. Nenhuma
  credencial de banco ou de WhatsApp de sistema.
- **LLM:** Gemini em **plano pago** (o gratuito pode usar o conteúdo).
- **Retenção:** 90 dias de texto completo, depois só metadados; apagamento sob pedido do titular.
- **Log:** sem texto de mensagem nem argumento de ferramenta no log contínuo (`LOG_DEBUG` pontual,
  como no Smaug).

## 6. Configuração

Ver `.env.example`. Configuração **de agente** (persona, instruções, sistema, OpenAPI, segredos por
referência de env, modelo): **forma a decidir** (§10, A4).

## 7. Operação

Container na VPS da Sola. **A decidir:** rede interna apenas (F1) ou rota pública pelo Traefik (A7).
`/health`, log JSON (`slog`), métricas de custo por agente no log por turno.

## 8. Critérios de aceite (F1)

Preenchidos em [CHECKLIST-ACEITE.md](CHECKLIST-ACEITE.md) quando o contrato fechar. Base: bateria fixa
de conversas do agente Eliza (marcar, remarcar, cancelar, confirmar, desconhecido, ambíguo, injeção de
prompt, fora de escopo, sistema fora do ar, rajada, intervenção humana).

## 9. Fora do escopo — backlog

Canal web e modo anônimo (F2, site); asserção do motor e envio de arquivo (F3, Axios); confirmação
antes de gravar (F4, SolaBridge); painel de configuração; API oficial do WhatsApp.

## 10. Decisões abertas da F1

| # | Questão | Recomendação | Trade-off |
|---|---|---|---|
| A1 | **Base do agens:** SQLite próprio (como o Smaug), Postgres próprio, ou schema `atendente` no Supabase do Eliza (E4) | **SQLite próprio** num volume, com backup diário. Revê a E4: com vários sistemas, guardar conversa de todos no banco do Eliza não faz sentido | Backup e superfície LGPD separados do Eliza; migrar para Postgres se um dia houver mais de uma instância |
| A2 | **Modelo padrão:** 2.5 Flash-Lite (E3) ou 2.5 Flash | **2.5 Flash, temperatura 0, raciocínio 0.** O Smaug registrou (29/09) que o Flash-Lite devolvia resposta vazia depois de `functionResponse`. Revê a E3 | ~US$ 0,015–0,03 por conversa em vez de ~0,006; a bateria de avaliação decide se o Lite volta |
| A3 | **Código do Smaug:** copiar os pacotes genéricos ou extrair um módulo Go comum agora | **Copiar** (`llm`, `worker`, `transcricao`, esqueleto de `agent`), sem tocar no Smaug em produção. Unificar quando a duplicação doer | Correção num lado não chega no outro sozinha |
| A4 | **Config de agente:** arquivo versionado (`agentes/eliza.yaml`) ou tabela + API admin | **Arquivo por agente** no repo, com segredos por nome de variável de ambiente | Mudar persona exige deploy (aceitável na F1) |
| A5 | **Conector OpenAPI:** ler a especificação em runtime ou gerar cliente em build (E1) | **Runtime**: baixa a spec no boot, guarda em cache, recarrega por sinal; ferramentas = operações `x-atendente: true`. Revê a parte "cliente gerado" da E1 | Erro de spec aparece no boot, não na compilação; o boot falha se a spec for inválida |
| A6 | **Marcação no Eliza:** adicionar `x-atendente: true` às operações da OpenAPI do Autoatendimento | Sim — mudança pequena em `web/lib/openapi/autoatendimento.ts` | Toca o Eliza (um PR) |
| A7 | **Rede na F1:** só rede Docker interna com o Eliza, ou rota pública | **Interna** (o Eliza está na mesma VPS); rota pública só na F2 (widget) | Testar de fora exige túnel |
| A8 | **Imagem na F1:** aceitar (foto de documento, print de horário) ou responder "só texto e áudio" | **Responder fixo** na F1 | Menos útil; evita custo e risco de dado sensível em imagem |
