# 01 — Domínio e schema

> **Esqueleto.** A1 e A4 fechadas (CONTRATO §10); B1 e B2 também (SQL portável, ver §0).

## 0. Chaves (A1)

Toda entidade com dado de conversa leva **`agente` + `tenant`**. `tenant` é a conta do cliente
dentro do sistema (no Eliza, a `org` do ticket; no site, um tenant fixo `sola`). Nenhuma consulta
atravessa tenants. Acesso à base pela interface `Store` e trava por conversa pela interface `Trava`:
SQLite e trava em memória agora; Postgres e advisory lock quando houver réplicas (B1).

---

## 1. Agente

Um sistema configurado no motor. Campos previstos: `id` (`eliza`), nome exibido, persona e
instruções, URL da OpenAPI, base URL do sistema, modo de identidade (`encaminhado` | `assercao` |
`anonimo`), referência aos segredos (nomes de env), modelo e limites (iterações, turnos por contato
por dia), tokens do fastpath, limites de áudio.

## 2. Conversa e contato

Chave da conversa = `agente + tenant + contato` (telefone no WhatsApp; sessão no widget). Estado:
ativa ou **pausada** até um instante (intervenção humana, `deMim`): 12 h por padrão, configurável
por sistema; cada nova mensagem `deMim` reinicia o prazo (M1).

## 2.1 Mensagem e mídia (A8)

Mensagem = texto + `anexos[]`, cada anexo com `tipo` (`audio` | `imagem` | `documento` | `video` |
`figurinha`), `mime`, `tamanho` e `ref` (identificador no canal — o agens não baixa nem guarda o
arquivo). Política de mídia por agente: por tipo, `processar` (v1: só `audio`, via transcrição) ou
`resposta_fixa`. Ligar imagem depois é mudar a política, não o schema.

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
