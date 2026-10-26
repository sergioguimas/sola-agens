# Rodada 4 — canal web na página pública do Eliza (`/marcar/[slug]`)

> **Status:** rodada de 26/10/2026 com o Sérgio. **W1–W10 fechadas**, todas pela recomendação
> (W1, W3, W5 e W10 escolhidas uma a uma; W2, W4 e W6–W9 confirmadas como propostas). Resumo no
> CONTRATO §10 (rodada 4); lado do Eliza em `Eliza/docs/contratos/autoatendimento/08-canal-web.md`.
> Cada linha traz a alternativa descartada e o motivo.
> **Origem:** 5 pessoas testaram a página pública no celular e pararam no resumo sem concluir. O layout
> foi corrigido no Eliza (commit 7ca0391, barra fixa com CTA). Este chat é o passo seguinte: ajudar o
> cliente a **escolher por conversa** e devolver a escolha **pronta no formulário**.
> **Avaliado e descartado antes desta rodada:** `alibaba/page-agent` (agente JS que opera o DOM). Numa
> página anônima o prompt é montado no navegador, então o proxy do LLM vira relay aberto; os selects
> Radix não respondem a clique sintético; e o conteúdo da etapa 3 (nome, telefone, CPF) iria ao
> provedor do LLM. Ver nó `Github-Repos/mapa/nos/alibaba__page-agent.md` (vale para o painel logado,
> não para página pública).

## O que é

Um botão flutuante no canto inferior da `/marcar/[slug]` abre um chat. O cliente conversa ("queria
fazer as unhas na sexta à tarde, quem atende?"), o agente consulta serviços, profissionais e horários
livres **da própria org** e, quando a escolha fecha, **preenche o formulário** (serviço, profissional,
data, hora) e leva o cliente à etapa 3. Quem confirma é sempre o cliente, no "Agendar agora".

```txt
navegador (/marcar/slug)                Eliza (Next)                          sola-agens
 chat ──POST /api/marcar/{slug}/chat──▶  rate limit, sessão, ticket visitante
                                         ──POST /a/eliza/v1/web (assinado)──▶ turno síncrono
                                                                               ├─ GET obterContexto (visitante)
                                                                               ├─ listarServicos / listarProfissionais / consultarHorarios
                                                                               └─ ferramenta de canal: preencher_agendamento
 ◀── { resposta, acoes[] } ──────────────◀── { resposta, acoes[] } ──────────┘
 aplica acoes no formulário (valida contra as listas e os horários antes)
```

## Decisões abertas

| # | Recomendação | Alternativa descartada | Motivo |
|---|---|---|---|
| W1 ✅ | **Escopo: aconselhar e pré-preencher. O chat nunca grava.** Ferramentas: `listarServicos`, `listarProfissionais`, `consultarHorarios` + a ferramenta de canal `preencher_agendamento` (W4) | Agendar pelo chat | Gravar exige identidade verificada (telefone com código), que a página não tem. O formulário já valida, pede os dados e cria o `pending`; duplicar esse caminho no chat é o risco que o D9 do Eliza fechou (toda escrita por `lib/domain`) |
| W2 ✅ | **Identidade: modo anônimo com "ticket de visitante" emitido pelo Eliza** ao renderizar a página: `{ v:1, tipo:"visitante", org, sess, iat, exp }`, sem `tel` nem `inst`, TTL 30 min. As rotas de catálogo e horários aceitam esse tipo; todas as outras recusam com `TICKET_INVALID` | Agens chamar o catálogo público passando a org como argumento | A org viria do modelo ou do corpo, e identidade nunca vem do LLM (CONTRATO §5). O ticket reaproveita o HMAC e o autenticador que já existem; muda só o payload e uma lista de rotas permitidas |
| W3 ✅ | **Navegador → Eliza → agens, com o agens só na rede interna** (mantém a A7). O Eliza expõe `POST /api/marcar/{slug}/chat` (mesma origem, sem CORS), aplica o rate limit (W6), emite e renova o ticket e encaminha assinado como no modo encaminhado | Navegador falar direto com uma rota pública do agens | Sem rota pública, CORS nem segredo no navegador. O custo é um salto a mais (~ms na mesma rede Docker). O site da Sola, que é estático, vai precisar de rota pública na F2b de todo jeito; ela não precisa nascer agora |
| W4 ✅ | **Resposta síncrona com ações:** o agens responde no corpo da própria chamada `{ resposta, acoes[] }`, sem streaming na v1. `preencher_agendamento` é uma **ferramenta de canal**: declarada pelo canal web (não pela OpenAPI), o motor não a executa, só a coloca em `acoes`. O navegador confere cada id contra as listas da página e o horário contra `getAvailableSlots` antes de aplicar; o que não bater é ignorado e o chat avisa | Streaming (SSE); o agens devolver só texto e o cliente preencher na mão | Síncrono cabe no turno de 90 s e reaproveita o loop da F1 sem mudar o worker. SSE melhora a percepção de espera, mas mexe no worker e no proxy do Eliza; fica para quando a latência medida justificar. Só texto não resolve o problema de origem (a pessoa não termina as escolhas) |
| W5 ✅ | **Retenção de 7 dias** para conversas anônimas, depois só metadados | Os 90 dias da plataforma | Sem titular identificado não há como atender um pedido de apagamento, e o visitante pode digitar dado pessoal mesmo sem ser pedido. Guardar pouco é a única proteção que funciona aqui. A métrica (turnos, custo, se preencheu) continua nos metadados |
| W6 ✅ | **Limites de abuso em camadas:** no Eliza, 20 mensagens por sessão, 60 por IP/hora, 500 caracteres por mensagem; no agens, teto diário de turnos por org (padrão 300, configurável). Sem CAPTCHA na v1; o Turnstile entra se o log mostrar abuso | CAPTCHA desde o início | O prompt e as ferramentas ficam no servidor, então não há relay aberto: o pior caso é custo, e ele fica limitado pelos tetos (~US$ 0,03/conversa × 300 = ~US$ 9/dia/org no teto). CAPTCHA logo de cara atrapalha justamente quem já trava no fluxo |
| W7 ✅ | **O chat não pede dado pessoal.** O prompt do canal proíbe pedir nome, telefone, CPF e dado de saúde ("isso você preenche no formulário"). Aviso fixo no topo do chat: assistente automático, não informe documentos | Deixar o chat coletar os dados | Os dados já têm lugar e base legal no formulário. O chat vai a um provedor externo (Gemini pago): quanto menos dado pessoal passar por ele, menor a exposição |
| W8 ✅ | **Ligado por org:** coluna `canal_web boolean default false` em `autoatendimento_config`. O botão só aparece com `ativo = true` **e** `canal_web = true` | Reaproveitar só o `ativo` | Uma org pode querer o bot no WhatsApp e não na página, ou o contrário. Uma coluna custa uma migration pequena |
| W9 ✅ | **UI: componente React dentro do Eliza** (botão flutuante acima da barra fixa do celular; bottom sheet em tela cheia no celular, painel lateral no desktop; cores do tema do nicho) | Script de embed genérico agora (Shadow DOM, configurável) | Só o Eliza consome por enquanto. O embed genérico é necessário para o site estático da Sola e nasce na F2b, já com um consumidor real para guiar o desenho |
| W10 ✅ | **Ordem: dividir a F2.** F2a = canal web + modo anônimo + ferramenta de canal + agente `/marcar` (este documento). F2b = conhecimento estático + embed genérico + agente do site. F2a começa **depois do aceite da F1** (motor validado com Gemini real) | Manter a F2 inteira com o site primeiro (P6) | A F2a usa a OpenAPI que já existe e dá prova de canal web com tráfego real do Eliza. O site perde algumas semanas na fila. A F1 antes porque é o mesmo motor: um bug do loop apareceria nos dois canais |

## O que muda em cada lado (para o contrato, depois de fechar)

**Eliza**

- `TicketPayload` ganha a variante `tipo: "visitante"` (sem `tel`/`inst`); `autenticarTicket` aceita
  essa variante só em `servicos`, `profissionais`, `horarios` e `contexto`.
- `obterContexto` com visitante: `identificacao.situacao = "visitante"`, sem `agendamentos` nem
  `aguardandoConfirmacao`.
- Rota `POST /api/marcar/{slug}/chat`: resolve a org pelo slug, confere `ativo && canal_web`, aplica
  os limites da W6, emite/renova o ticket e encaminha para o agens.
- Migration: `canal_web` em `autoatendimento_config`.
- `public-booking-form.tsx`: botão + chat; função `aplicarAcao` que valida e chama `form.setValue`.
  **Armadilha conhecida:** trocar a data dispara o efeito que recarrega os horários e zera `time`, então
  a hora tem de ser aplicada **depois** de os horários carregarem. Em seguida a função leva o cliente
  para a etapa da primeira escolha que faltar.

**sola-agens**

- Entrada `POST /a/{agente}/v1/web` (assinada como a encaminhada), que responde no corpo.
- Modo de identidade `anonimo` no registro do agente; o conjunto de ferramentas é filtrado por modo
  (anônimo: só operações marcadas como públicas, por exemplo `x-atendente-publico: true` na OpenAPI).
- Ferramentas de canal: declaradas pelo canal, capturadas pelo loop e devolvidas em `acoes`.
- Prompt do canal web (W7) na camada sistema → tenant (A4); fastpath desligado no anônimo.
- Retenção por modo (W5) e teto diário por org (W6) na manutenção e no worker.

## Avaliação antes de abrir

Bateria fixa (padrão da plataforma), no mínimo: escolhe tudo numa frase; escolhe aos poucos; serviço
inexistente; horário cheio (oferece alternativa); pergunta de preço; tenta agendar pelo chat (explica
que é no formulário); manda CPF (não repete nem guarda); pede outra org ou tenta injetar prompt;
pergunta fora do tema. Medir custo, latência (alvo: menos de 6 s no p90) e a taxa de "preencheu →
concluiu" contra a página sem chat.
