# Multi-stage build for the Harporis getter.
# Build context must be the repo root (not services/getter), so the
# contracts/ module is available at the same relative path the getter's
# go.mod 'replace' directive expects.

FROM golang:1.26-alpine AS build

RUN apk add --no-cache git

WORKDIR /src

# Pull deps first to leverage Docker's layer cache. go.mod/go.sum changes
# trigger a re-download; source-only changes don't.
COPY contracts/go.mod contracts/go.sum ./contracts/
COPY kit/go.mod kit/go.sum ./kit/
COPY services/getter/go.mod services/getter/go.sum ./services/getter/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd services/getter && go mod download

# Now bring the source in.
COPY contracts ./contracts
COPY kit ./kit
COPY services/getter ./services/getter

# CGO_ENABLED=0 → fully static binary, ldflags strip debug info for smaller image.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
    cd services/getter && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/getter ./cmd/getter && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/getter-cli ./cmd/getter-cli

# ---------- runtime ----------
FROM alpine:3.20

# git: needed at runtime — getter shells out to git CLI for clone/cat-file/etc.
# openssh-client: needed for ssh:// remotes (SSH key & ssh-agent auth modes).
# ca-certificates: required for HTTPS clones.
RUN apk add --no-cache git ca-certificates openssh-client && \
    addgroup -S harporis && \
    adduser -S -G harporis -h /home/harporis harporis && \
    mkdir -p /var/lib/harporis/scans && \
    chown -R harporis:harporis /var/lib/harporis /home/harporis

COPY --from=build /out/getter /usr/local/bin/getter
COPY --from=build /out/getter-cli /usr/local/bin/getter-cli
COPY services/getter/config /etc/harporis

USER harporis
WORKDIR /home/harporis

# Mounted host repos often have a UID that doesn't match the `harporis`
# user inside the container, so git refuses to operate on them ("dubious
# ownership"). Whitelisting all paths is acceptable here: the getter only
# READS the repo, never modifies it.
RUN git config --global --add safe.directory '*'

# gRPC + Prometheus metrics
EXPOSE 50051 9100

# Defaults — the YAML config picks these up via ${VAR:-default} substitution.
ENV NATS_URL=nats://nats:4222 \
    WORK_DIR=/var/lib/harporis/scans

ENTRYPOINT ["/usr/local/bin/getter"]
CMD ["--config", "/etc/harporis/getter.yaml", "--metrics-port", "9100"]
