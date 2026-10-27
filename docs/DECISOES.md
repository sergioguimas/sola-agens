# sola-agens — decisões de origem

> **O que é:** plataforma interna da Sola para colocar um atendente (bot com LLM) em qualquer
> sistema da Sola, consumindo a **OpenAPI** que o próprio sistema publica. Não é um produto
> comercial de chatbot.
> **Status:** decidido; modelo de documentação montado a partir do Smaug (09/10). Rodada 2 (09/10) fechou A1–A8 e
> M1–M4 do CONTRATO §10 (revendo E1, E3 e E4 abaixo). Rodada 3 fechou B1–B4. Specs, plano e aceite
> preenchidos (09/10); decisões de especificação S1–S8 em specs/05 §9.
> **Rodada de planejamento:** 2026-10-09, com o Sérgio. Antecedentes:
> `Eliza/docs/ANALISE_ATENDENTE.md` e `Eliza/docs/CHATBOT.md` (decisões E1–E6).

## Por que existe

Os sistemas da Sola têm stacks diferentes e cada um vai querer um atendente:

| Sistema | Stack | O que o atendente faz |
|---|---|---|
| Eliza | Next.js + Supabase | Agendar, remarcar, cancelar, cadastro do cliente final |
| Site da Sola | Next.js estático (sem servidor) | Tirar dúvidas e captar contato |
| Axios Calc | Next.js 15 | Gerar orçamento por mensagem |
| SolaBridge | Laravel (PHP) + React | Cliente lança movimentação pelo WhatsApp |

Escrever um bot por sistema repetiria fila, LLM, ferramentas, histórico e segurança quatro vezes.
Um motor que lê a OpenAPI de cada sistema e fala HTTP é o único jeito de atender Next, Laravel e
site estático com o mesmo código.

## Arquitetura

```txt
canais                 motor (Go)                              fontes
WhatsApp ──┐   fila, dedupe, rajada, lock por conversa    ┌── OpenAPI do sistema (ferramentas)
 (Evolution)├─▶ LLM + ferramentas + fastpath            ──┼── conhecimento estático (site, FAQ)
widget web ─┘   confirmação antes de gravar               └── identidade: encaminhado | asserção | anônimo
                envio de arquivo, pausa humana, escalonamento
                log por turno e custo, por "agente"
```

- **Agente** = um sistema configurado no motor: persona, instruções, ferramentas liberadas, canal,
  número, modelo, modo de identidade.
- **Uma instância do motor na VPS da Sola** atende todos os agentes da Sola, com dados separados por
  agente. Outra empresa (ex.: Geti) teria a sua instância, nunca a mesma.
- **Ponto de partida do código:** o motor do Smaug (`redmilab/servicos/smaug`): cliente Gemini,
  registro de ferramentas, fastpath, fila, áudio, imagem. Gateway whatsmeow de referência em
  `redmilab/servicos/wa`.

## Contrato de integração (o que cada sistema implementa)

1. **OpenAPI voltada ao atendente**, com as operações liberadas marcadas (`x-atendente: true`). A
   descrição de cada operação é a instrução da ferramenta para o LLM.
2. **Identidade do cliente final verificada pelo sistema**, nunca informada pelo LLM:
   - **encaminhado:** o sistema recebe a mensagem e assina um ticket (Eliza, já pronto);
   - **asserção do motor:** o motor recebe a mensagem e envia o contato assinado com segredo por
     sistema; o sistema confere um HMAC (padrão para os próximos sistemas);
   - **anônimo:** visitante sem identidade; só ferramentas públicas e conhecimento estático.
3. **Posse no sistema:** o cliente só enxerga o que é dele. A regra fica no sistema, nunca no motor.
4. **Erros com mensagem para o usuário final** (recomendado; o motor trata erro genérico).

Para sistemas Next.js, o que foi feito no Eliza vira modelo em `Projetos/_templates/`: núcleo HTTP
(`lib/http`), gerador de OpenAPI a partir dos Zod e verificação de assinatura. Laravel usa gerador
de OpenAPI próprio (ex.: Scribe).

## Decisões

| # | Decisão | Alternativa descartada | Motivo |
|---|---|---|---|
| P1 | **Plataforma interna da Sola**, não produto comercial | Vender como chatbot | O objetivo é replicar atendente nos sistemas da Sola. Sai do escopo: cobrança, onboarding, painel comercial |
| P2 | **Ferramentas lidas da OpenAPI** de cada sistema, com o contrato de integração acima | Adaptador em código por sistema | Stacks diferentes (Next, Laravel, estático); encaixar sistema novo vira configuração. Adaptador em código fica só como saída para legado |
| P3 | **Três modos de identidade**: encaminhado (Eliza), asserção do motor (padrão dos próximos), anônimo (site) | Um modo só | O Eliza já está pronto no encaminhado; os outros não precisam lidar com WhatsApp; o site não tem login |
| P4 | **Go**, partindo do motor do Smaug | Node/TS, Python | Motor e gateway já existem em Go; binário único de ~20–40 MB; a linguagem é indiferente para quem integra, porque o contrato é OpenAPI + HTTP |
| P5 | Nome **`sola-agens`**, em `Projetos/sola-agens`, repositório próprio (como os outros produtos da Sola) | Dentro do Eliza | É de todos os sistemas da Sola |
| P6 | Ordem dos agentes: **Eliza → Site → Axios Calc → SolaBridge** | Eliza → Axios → Bridge → Site | Decisão do Sérgio |
| E6 | A Evolution do Eliza roda **na VPS da Sola**, sem relação com a Geti | — | Confirmado pelo Sérgio em 2026-10-09 |

Herdadas da análise do Eliza e válidas para a plataforma inteira (com as revisões da rodada 2 no
CONTRATO §10):

- **LLM:** Gemini em **plano pago** (o gratuito pode usar o conteúdo), raciocínio desligado,
  **fastpath** determinístico na frente. Modelo padrão: ~~2.5 Flash-Lite~~ ~~2.5 Flash~~ (A2 — o
  Flash-Lite falhou depois de resultado de ferramenta no Smaug, 29/09) **3.6 Flash** (A9 — o 2.5 Flash
  fez 10/17 na 1ª avaliação real, 26/10; o 3.6 fez 34/34).
- **Base:** ~~schema `atendente` no Supabase do Eliza (E4)~~ **base própria do agens, multi-tenant
  (`agente + tenant`) desde o dia 1** (A1).
- **Conector:** ~~cliente gerado da OpenAPI (E1)~~ **OpenAPI lida em tempo de execução** (A5).
- **Configuração:** em camadas, padrão do motor → sistema → tenant; cada sistema é dono da sua (A4, M4).
- **Avaliação:** bateria fixa de conversas por agente antes de abrir para usuário real e a cada troca
  de modelo ou prompt.
- **Retenção:** 90 dias de texto completo, depois só metadados; apagamento sob pedido do titular.
- **Auditoria:** log por turno no motor (ferramentas, argumentos, modelo, tokens, custo), além do log
  de cada sistema.
- **WhatsApp não oficial** (Evolution/whatsmeow) pode ter número banido: números da Sola dedicados,
  nunca o principal. API oficial da Meta só se algum atendente crescer.

## Fases

| Fase | Entrega | Prova |
|---|---|---|
| **F1** | Motor + conector OpenAPI + modo **encaminhado** + agente **Eliza** | A API do Eliza já existe e foi testada: valida o motor sem mexer em sistema |
| **F2a** | Canal **web** (via Eliza) + modo **anônimo** + **ferramenta de canal** + chat da `/marcar` do Eliza (rodada 4, W10) | Canal novo com tráfego real, sobre a OpenAPI que já existe |
| **F2b** | **Conhecimento estático** + **embed genérico** + agente do **site** | Agente sem OpenAPI de escrita, em site estático |
| **F3** | Modo **asserção do motor** + **envio de arquivo** + agente **Axios Calc** | Primeiro sistema que implementa o contrato de integração do zero |
| **F4** | **Confirmação antes de gravar** + agente **SolaBridge** | Laravel e dado fiscal: independência de stack e segurança de escrita |
