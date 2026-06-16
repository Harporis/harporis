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
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/getter ./cmd/getter

# ---------- runtime ----------
FROM alpine:3.20

# git: needed at runtime — getter shells out to git CLI for clone/cat-file/etc.
# openssh-client: needed for ssh:// remotes (SSH key & ssh-agent auth modes).
# ca-certificates: required for HTTPS clones.
RUN apk add --no-cache git ca-certificates openssh-client && \
    addgroup -S harporis && \
    adduser -S -G harporis -h /home/harporis harporis && \
    mkdir -p /var/lib/harporis/scans && \
    chown -R harporis:harporis /var/lib/harporis /home/harporis && \
    # Mounted host repos often have a UID that doesn't match the runtime
    # user, so git refuses to operate on them ("dubious ownership").
    # Whitelist all paths in the SYSTEM gitconfig (/etc/gitconfig) so it
    # applies to whatever UID compose injects — a per-user --global config
    # in /home/harporis wouldn't be read by an arbitrary host UID. The
    # getter only READS repos, never modifies them.
    git config --system --add safe.directory '*' && \
    # The compose file runs this container as an arbitrary host UID (so the
    # read-only $HOME bind-mount is traversable). That UID has no passwd
    # entry, which breaks OpenSSH ("No user exists for uid N") on ssh://
    # clones. World-writable /etc/passwd lets the entrypoint synthesize an
    # entry for whatever UID is injected at runtime.
    chmod 0666 /etc/passwd

COPY --from=build /out/getter /usr/local/bin/getter
COPY services/getter/config /etc/harporis
COPY services/getter/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

USER harporis
WORKDIR /home/harporis

# gRPC + Prometheus metrics
EXPOSE 50051 9100

# Defaults — the YAML config picks these up via ${VAR:-default} substitution.
ENV NATS_URL=nats://nats:4222 \
    WORK_DIR=/var/lib/harporis/scans

# entrypoint.sh synthesizes a passwd entry for the runtime UID (for SSH),
# then exec's getter with the CMD args.
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["--config", "/etc/harporis/getter.yaml", "--metrics-port", "9100"]
