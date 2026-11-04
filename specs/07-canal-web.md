# 07 — Canal web (F2a): chat da página pública do Eliza

> **AS-IS** desde 03/11/2026, depois da F2c (T12 invertida, specs/06 S18). Decisões de origem:
> [docs/RODADA-4-CANAL-WEB-MARCAR.md](../docs/RODADA-4-CANAL-WEB-MARCAR.md) (W1–W10). Lado do sistema:
> `Eliza/docs/contratos/autoatendimento/08-canal-web.md` (C14–C21), que define o corpo das mensagens.
> Decisões tomadas ao escrever esta spec: §6 (S35–S40).

## 1. O que é

Uma entrada síncrona para visitante anônimo. O agente aconselha, consulta catálogo e horários e devolve
**ações** que preenchem o formulário. **Nunca grava** (W1). Sem fastpath, pausa, envio pelo sistema nem
escalonamento: não há cliente identificado nem WhatsApp neste canal.

## 2. Registro (`agentes/*.yaml`)

Bloco opcional. **Sem ele, `POST /a/{agente}/v1/web` responde `404 WEB_DESLIGADO`.**

```yaml
web:
  teto_turnos_dia_org: 300   # W6
  turno_timeout_s: 40        # abaixo dos 45 s do Eliza
```

Com o bloco, o boot exige ao menos uma operação `x-atendente-publico: true` na spec do sistema.

## 3. Entrada

`POST /a/{agente}/v1/web`, assinada como a encaminhada (`entrada.segredo_env`, 04 §1). Corpo e resposta
são os Zod `MensagemWeb` e `ChatWebResposta` do contrato 08 §6.2. Respostas:

| Status | Código | Quando |
|---|---|---|
| 200 | | `{ resposta, acoes[] }` |
| 400 | `CORPO_INVALIDO` | JSON, campos ou contato fora de `web:{uuid}` |
| 401 | `NAO_AUTORIZADO` | assinatura |
| 404 | `WEB_DESLIGADO` | agente sem o bloco |
| 409 | `DUPLICADA` | `mensagemId` repetido |
| 429 | `TETO_DIARIO` | teto de turnos da org no dia (AGENS_TZ) |
| 503 | `INDISPONIVEL` | LLM, sistema ou prazo |

Qualquer falha vira, no Eliza, a mensagem fixa do chat. Nada é gravado em falha: reenviar não duplica.

## 4. Turno

1. Teto do dia (S38), trava por conversa, `obterContexto` com o ticket de visitante. Situação diferente de
   `visitante` aborta: canal e ticket precisam concordar.
2. **Mascarar** documento e telefone no texto (S36), antes do LLM e da base.
3. `PromptWeb` (W7) com a janela de memória, as operações públicas e `preencher_agendamento`.
4. Só ferramenta pública executa (S37). `preencher_agendamento` é do canal: o motor valida formato, junta
   as chamadas do turno numa ação só e devolve em `acoes`.
5. Grava cliente, agente e turno. Sem `acoes` no histórico: o `formulario` da próxima mensagem é a verdade
   (C20).

## 5. Retenção

Texto de conversa `web:` zera em **7 dias** (W5), no mesmo job da retenção. Turnos ficam, sem texto.

## 6. Decisões desta spec

| # | Decisão | Motivo |
|---|---|---|
| S35 | **Turno próprio** (`agent/web.go`), não o `turnoExec` do WhatsApp | O de lá é feito de envio, pausa, fastpath e escalonamento, que aqui não existem. Compartilham prompt-base, ferramentas e store |
| S36 | **Documento e telefone são mascarados no motor**, antes do LLM e da base | W7 pede que o chat não peça, mas o visitante pode digitar. "Não repete nem guarda" passa a valer por código |
| S37 | **Ferramenta fora da lista pública é recusada no motor**, mesmo que o modelo a chame | O Eliza recusa o ticket, mas identidade e permissão não dependem do modelo acertar |
| S38 | Teto conta `turnos` da org com contato `web:` no dia | Sem tabela nova; turno é o que custa |
| S39 | Uma ação por turno, com os campos das chamadas somados | O formulário tem um estado só; várias ações do mesmo turno disputariam o `setValue` |
| S40 | Resposta em texto puro, até 2.000 caracteres | O chat web não renderiza markdown, e o Zod do Eliza recusa acima disso, o que derrubaria a resposta inteira |
