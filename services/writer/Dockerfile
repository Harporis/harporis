# Multi-stage build for the Harporis writer.
# Build context MUST be the repo root, so the contracts/ and kit/ modules
# are available at the same relative paths the writer's go.mod replace
# directives expect.

FROM golang:1.26-alpine AS build

RUN apk add --no-cache git

WORKDIR /src

COPY contracts/go.mod contracts/go.sum ./contracts/
COPY kit/go.mod kit/go.sum ./kit/
COPY services/writer/go.mod services/writer/go.sum ./services/writer/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd services/writer && go mod download

COPY contracts ./contracts
COPY kit ./kit
COPY services/writer ./services/writer

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
    cd services/writer && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/writer ./cmd/writer && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/writer-rebuild ./cmd/rebuild

# ---------- runtime ----------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S harporis && \
    adduser -S -G harporis -h /home/harporis harporis && \
    mkdir -p /var/lib/harporis/findings && \
    chown -R harporis:harporis /var/lib/harporis

COPY --from=build /out/writer /usr/local/bin/writer
COPY --from=build /out/writer-rebuild /usr/local/bin/writer-rebuild
COPY services/writer/config /etc/harporis

USER harporis
WORKDIR /home/harporis

EXPOSE 9102

ENV NATS_URL=nats://nats:4222 \
    LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/writer"]
CMD ["--config", "/etc/harporis/writer.yaml"]
