# 02 — Ferramentas e prompt

> **Esqueleto.** No Smaug as ferramentas são fixas em Go (`agent/tools_*.go`). Aqui elas vêm da
> OpenAPI de cada sistema (decisão P2). Preencher depois de A5 e A6.

---

## 1. Da OpenAPI para ferramenta

- Fonte: a especificação do sistema (Eliza: `/api/v1/autoatendimento/openapi.json`).
- Só entram operações com `x-atendente: true` (A6).
- Nome da ferramenta = `operationId`; descrição = `summary` + `description` da operação.
- Parâmetros = união de path, query e body (JSON Schema da spec), **exceto** os que são de
  identidade ou transporte (headers de autenticação, ticket, versão), que o conector injeta.
- Resposta para o LLM: o `data` do envelope; em erro, `{ erro: code, mensagem: message, ...details }`
  (equivale ao `ToolError` do Smaug — o loop continua e o LLM explica ao cliente).

## 2. Ferramentas internas do motor

Independentes de sistema: `chamar_humano` (escalonamento), e no futuro `enviar_arquivo` (F3).
**A preencher.**

## 3. Prompt

Prompt base do motor (segurança, formato WhatsApp, nunca inventar identificador, usar sempre as
ferramentas para dado vivo) + persona e instruções do agente + contexto do turno (data/hora em
`America/Sao_Paulo`; para o Eliza, o resultado de `GET /contexto`). Estrutura de seções herdada do
Smaug: PERSONA, CONTEXTO, REGRA DE SEGURANÇA, COMO AGIR, FORMATO DA RESPOSTA. **A preencher.**

## 4. Fastpath

Herdado do Smaug (`fastpath.go`): respostas resolvidas sem LLM. Para o Eliza: "sim"/"confirmo" e
"cancelar" respondendo a um lembrete. **A preencher:** tokens por agente.
