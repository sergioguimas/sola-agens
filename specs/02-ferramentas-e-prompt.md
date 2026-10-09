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

Camadas (A4, M4): a REGRA DE SEGURANÇA é do motor e nenhuma camada a sobrepõe. PERSONA, tom,
vocabulário e respostas vêm do sistema e do tenant (no Eliza: `organizacao.termos` e, se B2 fechar
assim, `atendente` no `/contexto`); o texto do motor só entra quando a camada falta. Perfil gerado
pelo módulo de treinamento (CONTRATO §11) entra na camada do tenant.

## 4. Fastpath

Herdado do Smaug (`fastpath.go`): respostas resolvidas sem LLM. Para o Eliza: "sim"/"confirmo" e
"cancelar" respondendo a um lembrete. Fonte: **dicionário de respostas padrão** (M3), semeado com as
listas `CONFIRMATION_KEYWORDS`/`CANCELLATION_KEYWORDS` do webhook do Eliza e as mensagens de
`web/lib/domain/mensagens.ts`; sobrescrevível por sistema e tenant. **A preencher:** formato do
dicionário (entrada → intenção → resposta).
