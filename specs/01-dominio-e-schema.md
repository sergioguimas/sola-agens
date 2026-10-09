# 01 — Domínio e schema

> **Esqueleto.** Preencher depois das decisões A1 (base) e A4 (config de agente) do CONTRATO §10.

---

## 1. Agente

Um sistema configurado no motor. Campos previstos: `id` (`eliza`), nome exibido, persona e
instruções, URL da OpenAPI, base URL do sistema, modo de identidade (`encaminhado` | `assercao` |
`anonimo`), referência aos segredos (nomes de env), modelo e limites (iterações, turnos por contato
por dia), tokens do fastpath, limites de áudio.

## 2. Conversa e contato

Chave da conversa = `agente + contato` (telefone no WhatsApp; sessão no widget). Estado: ativa ou
**pausada** até um instante (intervenção humana, `deMim`).

## 3. Memória

Herdado do Smaug (`store/memoria.go`): janela por mensagens e por horas. **A preencher:** valores por
agente.

## 4. Eventos (dedupe)

Herdado do Smaug (`store/eventos.go`): `mensagemId` processado não reprocessa.

## 5. Log por turno (auditoria e custo)

Por turno: agente, conversa, mensagem de entrada (id), ferramentas chamadas (nome, status, código de
erro — **argumentos só em modo debug**), modelo, tokens de entrada e saída, custo estimado, latência.

## 6. Retenção

90 dias de texto completo; depois só metadados (sem texto); apagamento sob pedido do titular
(por contato e agente).

## 7. Schema

**A preencher** conforme A1.
