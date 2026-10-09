-- 0001_init.sql
CREATE TABLE schema_version (versao INTEGER NOT NULL);

CREATE TABLE conversas (
  agente        TEXT NOT NULL,
  tenant        TEXT NOT NULL,
  contato       TEXT NOT NULL,
  pausada_ate   INTEGER,              -- ms UTC; NULL = ativa
  config_json   TEXT,                 -- última config combinada recebida do sistema
  config_em     INTEGER,
  criada_em     INTEGER NOT NULL,
  atualizada_em INTEGER NOT NULL,
  PRIMARY KEY (agente, tenant, contato)
);

CREATE TABLE mensagens (
  id         TEXT PRIMARY KEY,
  agente     TEXT NOT NULL,
  tenant     TEXT NOT NULL,
  contato    TEXT NOT NULL,
  papel      TEXT NOT NULL CHECK (papel IN ('cliente','agente','humano')),
  conteudo   TEXT,                    -- NULL depois da retenção
  anexos     TEXT,                    -- JSON de []Anexo sem Ref
  criada_em  INTEGER NOT NULL
);
CREATE INDEX ix_mensagens_conversa ON mensagens (agente, tenant, contato, criada_em);
CREATE INDEX ix_mensagens_criada ON mensagens (criada_em);

CREATE TABLE eventos (
  agente      TEXT NOT NULL,
  mensagem_id TEXT NOT NULL,
  recebido_em INTEGER NOT NULL,
  PRIMARY KEY (agente, mensagem_id)
);

CREATE TABLE ecos (
  agente      TEXT NOT NULL,
  mensagem_id TEXT NOT NULL,
  criado_em   INTEGER NOT NULL,
  PRIMARY KEY (agente, mensagem_id)
);

CREATE TABLE turnos (
  id              TEXT PRIMARY KEY,
  agente          TEXT NOT NULL,
  tenant          TEXT NOT NULL,
  contato         TEXT NOT NULL,
  modo            TEXT NOT NULL CHECK (modo IN ('llm','fastpath','fixa','pausado','erro')),
  mensagem_ids    TEXT NOT NULL,      -- JSON []string
  modelo          TEXT,
  iteracoes       INTEGER NOT NULL DEFAULT 0,
  ferramentas     TEXT NOT NULL,      -- JSON []{nome,status,codigo}
  tokens_entrada  INTEGER NOT NULL DEFAULT 0,
  tokens_saida    INTEGER NOT NULL DEFAULT 0,
  custo_micro_usd INTEGER NOT NULL DEFAULT 0,
  latencia_ms     INTEGER NOT NULL DEFAULT 0,
  resultado       TEXT NOT NULL,
  escalou         INTEGER NOT NULL DEFAULT 0 CHECK (escalou IN (0,1)),
  criado_em       INTEGER NOT NULL
);
CREATE INDEX ix_turnos_agente_tenant ON turnos (agente, tenant, criado_em);

CREATE TABLE jobs (
  nome     TEXT NOT NULL,
  data_ref TEXT NOT NULL,             -- AAAA-MM-DD em AGENS_TZ
  feito_em INTEGER NOT NULL,
  PRIMARY KEY (nome, data_ref)
);
