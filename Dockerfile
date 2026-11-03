# syntax=docker/dockerfile:1
# Estágio de build: Go estático (sem cgo), módulos em cache própria.
# Versão exata: a tag 1.26 flutua e pode trazer um Go sem as correções de segurança.
FROM golang:1.26.10 AS build
# A imagem oficial define GOTOOLCHAIN=local, que ignora a linha toolchain do go.mod;
# com auto, o go.mod é a fonte da versão e uma atualização lá vale também aqui.
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# /out/data é criado aqui porque a imagem final não tem shell para mkdir/chown.
RUN mkdir -p /out/data \
  && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sola-agens .

# Imagem final: distroless estática, usuário não-root (uid/gid 65532).
# Fixada por digest para o build ser reprodutível; atualizar o digest junto com o Go.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /out/sola-agens /sola-agens
COPY agentes/ /agentes/
# /data pertence ao nonroot; um volume nomeado novo herda este dono.
COPY --from=build --chown=65532:65532 /out/data /data
ENV AGENS_DATA_DIR=/data \
    AGENS_AGENTES_DIR=/agentes \
    AGENS_LISTEN=0.0.0.0:4100
VOLUME /data
EXPOSE 4100
USER nonroot:nonroot
# Sem shell nem curl: o próprio binário faz o GET em /health (sem carregar a config).
# start-period cobre o boot (download da OpenAPI do Eliza, até 5 tentativas).
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["/sola-agens","health"]
ENTRYPOINT ["/sola-agens"]
