# Changelog

Todas as mudanças notáveis deste projeto são documentadas neste arquivo.

O formato é baseado em [Mantenha um Changelog](https://keepachangelog.com/pt-BR/1.1.0/),
e este projeto adere ao [Versionamento Semântico](https://semver.org/lang/pt-BR/spec/v2.0.0.html).

## [Não publicado]

### Modificado

- Pausa por intervenção humana passa de 12 h para **4 h** por padrão (`AGENS_PAUSA_HUMANA_HORAS`). Com 12 h, o cliente atendido à mão de manhã ficava sem o agente à tarde. Quem já tem `AGENS_PAUSA_HUMANA_HORAS=12` no `.env` precisa trocar o valor ou apagar a linha.

## [1.0.0] - 2026-11-02

### Adicionado

- Plataforma interna de atendente (bot LLM) da Sola em Go, com o Eliza como primeiro sistema: lê a OpenAPI do sistema em tempo de execução, atende por WhatsApp em texto e áudio, identifica o cliente pelo ticket do sistema, confirma e cancela pelo fastpath, pausa quando a equipe responde pelo celular e trata avisos do sistema (lembretes) sem pausar.
- Multi-tenant (agente + tenant) com configuração em camadas (motor → sistema → tenant), store SQLite com retenção de 90 dias e bateria de avaliação com Gemini real.

[Não publicado]: https://github.com/sergioguimas/sola-agens/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/sergioguimas/sola-agens/releases/tag/v1.0.0
