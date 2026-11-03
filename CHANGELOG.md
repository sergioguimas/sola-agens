# Changelog

Todas as mudanças notáveis deste projeto são documentadas neste arquivo.

O formato é baseado em [Mantenha um Changelog](https://keepachangelog.com/pt-BR/1.1.0/),
e este projeto adere ao [Versionamento Semântico](https://semver.org/lang/pt-BR/spec/v2.0.0.html).

## [Não publicado]

### Adicionado

- Chat de treino do atendente, primeira fase: a dona do estabelecimento conversa com o agens, que faz o papel de cliente em ensaios e guarda como ela responde. Vem desligado (bloco `treino` no registro do agente, com `ELIZA_TREINO_SECRET`) e ainda não muda o atendimento dos clientes. Rotas assinadas em `/a/{agente}/v1/treino/sessoes`.
- Aprendizado do chat de treino: ao encerrar, o agens extrai o jeito de escrever (tratamento, saudações, emojis, pontuação, tom, exemplos) e grava uma versão do perfil de estilo. Padrão só entra depois de aparecer em duas sessões ou de ser declarado, e afirmação de fato vira nota para revisar, nunca estilo. Sessão parada por 30 minutos é encerrada sozinha. O perfil ainda não chega ao atendimento.
- API do painel de treino: ler o perfil e como ele entraria no prompt, listar versões, observações e notas, voltar a uma versão, rejeitar e restaurar padrões, decidir notas e zerar o perfil. A assinatura dessas rotas cobre método e caminho, então vale só para a rota assinada.
- Avaliação do chat de treino com o modelo real: `go run ./avaliacao -treino`.

## [1.1.0] - 2026-11-02

### Modificado

- Pausa por intervenção humana passa de 12 h para **4 h** por padrão (`AGENS_PAUSA_HUMANA_HORAS`). Com 12 h, o cliente atendido à mão de manhã ficava sem o agente à tarde. Quem já tem `AGENS_PAUSA_HUMANA_HORAS=12` no `.env` precisa trocar o valor ou apagar a linha.

## [1.0.0] - 2026-11-02

### Adicionado

- Plataforma interna de atendente (bot LLM) da Sola em Go, com o Eliza como primeiro sistema: lê a OpenAPI do sistema em tempo de execução, atende por WhatsApp em texto e áudio, identifica o cliente pelo ticket do sistema, confirma e cancela pelo fastpath, pausa quando a equipe responde pelo celular e trata avisos do sistema (lembretes) sem pausar.
- Multi-tenant (agente + tenant) com configuração em camadas (motor → sistema → tenant), store SQLite com retenção de 90 dias e bateria de avaliação com Gemini real.

[Não publicado]: https://github.com/sergioguimas/sola-agens/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/sergioguimas/sola-agens/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/sergioguimas/sola-agens/releases/tag/v1.0.0
