#!/usr/bin/env bash
# Harporis one-command installer for the full stack
# (CLI + getter + scanner + writer + NATS JetStream).
#
# Usage:
#   bash scripts/install.sh                      # default: $HOME/.local
#   PREFIX=$HOME/.local bash scripts/install.sh
#   PREFIX=/usr/local sudo -E bash scripts/install.sh
#   bash scripts/install.sh --skip-stack         # CLI + deps only
#
# What it does:
#   1. ensures Go >= 1.26 (downloads to ~/.local/go if missing)
#   2. ensures Docker + compose v2 (offers to run get.docker.com)
#   3. builds harporis and installs to $PREFIX/bin
#   4. installs shell completion for your current shell
#   5. patches rc-file (idempotently) so PATH + completion work in new shells
#   6. brings up the stack: nats + getter + scanner + writer (unless --skip-stack)
#   7. runs `harporis doctor`

set -euo pipefail

GO_VERSION="${GO_VERSION:-1.26.0}"
PREFIX="${PREFIX:-$HOME/.local}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKIP_STACK=0

for arg in "$@"; do
  case "$arg" in
    --skip-stack) SKIP_STACK=1;;
    -h|--help) sed -n '1,20p' "$0"; exit 0;;
  esac
done

# ---- logging ----------------------------------------------------------------

if [ -t 1 ]; then
  C_BLUE='\033[34m'; C_GREEN='\033[32m'; C_AMBER='\033[33m'; C_RED='\033[31m'; C_DIM='\033[2m'; C_RESET='\033[0m'
else
  C_BLUE=''; C_GREEN=''; C_AMBER=''; C_RED=''; C_DIM=''; C_RESET=''
fi
log()  { printf "${C_BLUE}▸${C_RESET} %s\n" "$*"; }
ok()   { printf "${C_GREEN}✓${C_RESET} %s\n" "$*"; }
warn() { printf "${C_AMBER}!${C_RESET} %s\n" "$*"; }
die()  { printf "${C_RED}✗${C_RESET} %s\n" "$*" >&2; exit 1; }

# ---- helpers ----------------------------------------------------------------

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64;;
    aarch64|arm64) echo arm64;;
    *) die "unsupported architecture: $(uname -m)";;
  esac
}

detect_shell() {
  basename "${SHELL:-/bin/bash}"
}

rc_file() {
  case "$(detect_shell)" in
    zsh)  echo "$HOME/.zshrc";;
    bash) echo "$HOME/.bashrc";;
    fish) echo "$HOME/.config/fish/config.fish";;
    *)    echo "$HOME/.bashrc";;
  esac
}

# append_unique <file> <marker-regex> <block>
# Appends <block> to <file> only if no line matching <marker-regex> already exists.
append_unique() {
  local file="$1" marker="$2" block="$3"
  mkdir -p "$(dirname "$file")"
  touch "$file"
  if grep -qE "$marker" "$file"; then return; fi
  printf '\n# >>> harporis installer >>>\n%s\n# <<< harporis installer <<<\n' "$block" >> "$file"
}

# Major.minor of `go version` (e.g. 1.26). Empty if no go.
go_minor() {
  command -v go >/dev/null 2>&1 || return
  go version | awk '{print $3}' | sed 's/go//' | cut -d. -f1,2
}

# Returns 0 if $1 >= $2 (both "1.26" form).
version_ge() {
  printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

# ---- steps ------------------------------------------------------------------

ensure_go() {
  local v; v="$(go_minor)"
  if [ -n "$v" ] && version_ge "$v" "1.26"; then
    ok "Go $v already installed"
    return
  fi
  log "installing Go ${GO_VERSION} to ~/.local/go"
  local arch tarball
  arch="$(detect_arch)"
  tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
  mkdir -p "$HOME/.local"
  rm -rf "$HOME/.local/go"
  if ! curl -fsSL "https://go.dev/dl/${tarball}" | tar -xz -C "$HOME/.local"; then
    die "failed to download Go ${GO_VERSION} — install it manually then re-run"
  fi
  export PATH="$HOME/.local/go/bin:$PATH"
  append_unique "$(rc_file)" 'harporis: ensure go on PATH' \
    "# harporis: ensure go on PATH
export PATH=\"\$HOME/.local/go/bin:\$PATH\""
  ok "Go $(go version | awk '{print $3}') installed"
}

ensure_docker() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "Docker + compose v2 already present"
    return
  fi
  warn "Docker (with compose v2) not found"
  printf "${C_DIM}This will run the official Docker installer (https://get.docker.com). It requires sudo.${C_RESET}\n"
  local resp=""
  if [ -t 0 ]; then
    read -rp "Install Docker now? [Y/n] " resp
  fi
  case "${resp:-Y}" in
    [Nn]*) die "Docker is required to run the harporis stack — please install it yourself and re-run";;
  esac
  curl -fsSL https://get.docker.com | sh
  sudo usermod -aG docker "$USER" 2>/dev/null || true
  ok "Docker installed"
  warn "log out and back in (or 'newgrp docker') to use docker without sudo"
}

build_and_install() {
  log "building harporis"
  if ! ( cd "$REPO_ROOT" && make -C services/cli build completions ) >/tmp/harporis-install.log 2>&1; then
    cat /tmp/harporis-install.log >&2
    die "build failed (see output above)"
  fi
  install -d "$PREFIX/bin"
  install -m 0755 "$REPO_ROOT/services/cli/bin/harporis" "$PREFIX/bin/harporis"
  ok "installed $PREFIX/bin/harporis"

  # PATH
  case ":${PATH}:" in
    *":$PREFIX/bin:"*) :;;
    *)
      append_unique "$(rc_file)" 'harporis: ensure PREFIX bin on PATH' \
        "# harporis: ensure PREFIX bin on PATH
export PATH=\"$PREFIX/bin:\$PATH\""
      export PATH="$PREFIX/bin:$PATH"
      ;;
  esac
}

install_completion() {
  local sh; sh="$(detect_shell)"
  case "$sh" in
    bash) install_completion_bash;;
    zsh)  install_completion_zsh;;
    fish) install_completion_fish;;
    *)    warn "unknown shell '$sh' — skipping completion install";;
  esac
}

install_completion_bash() {
  local dir="$HOME/.bash_completion.d"
  install -d "$dir"
  install -m 0644 "$REPO_ROOT/services/cli/completions/harporis.bash" "$dir/harporis"
  append_unique "$HOME/.bashrc" 'harporis: source bash completion' \
    "# harporis: source bash completion
[ -f \$HOME/.bash_completion.d/harporis ] && . \$HOME/.bash_completion.d/harporis"
  ok "bash completion installed"
}

install_completion_zsh() {
  local dir="$HOME/.zsh/completions"
  install -d "$dir"
  install -m 0644 "$REPO_ROOT/services/cli/completions/_harporis" "$dir/_harporis"
  append_unique "$HOME/.zshrc" 'harporis: zsh completion fpath' \
    "# harporis: zsh completion fpath
fpath=(\$HOME/.zsh/completions \$fpath)
autoload -Uz compinit && compinit"
  ok "zsh completion installed"
}

install_completion_fish() {
  local dir="$HOME/.config/fish/completions"
  install -d "$dir"
  install -m 0644 "$REPO_ROOT/services/cli/completions/harporis.fish" "$dir/harporis.fish"
  ok "fish completion installed"
}

bring_up_stack() {
  if [ "$SKIP_STACK" -eq 1 ]; then
    warn "stack bring-up skipped (--skip-stack)"
    return
  fi
  # `docker info` is a lightweight permission probe; on fresh installs
  # the user is in the docker group but has not yet re-logged in, so the
  # current shell still gets "permission denied" on the socket. We fall
  # back to sg(1) to enter the group for the duration of the compose
  # invocation — same trick the official Docker docs recommend.
  local docker_runner="docker"
  if ! docker info >/dev/null 2>&1; then
    if id -nG "$USER" | tr ' ' '\n' | grep -qx docker; then
      log "user is in docker group but new group not yet active — using sg"
      docker_runner='sg docker -c "docker"'
    else
      warn "docker socket not reachable as $USER and user not in docker group; skipping stack bring-up"
      warn "fix: sudo usermod -aG docker $USER && newgrp docker, then re-run"
      return
    fi
  fi
  log "bringing up stack (nats + getter + scanner + writer)"
  # UID is readonly in bash, so we use env(1) to set it for the subprocess
  # only. The compose file reads ${UID:-1000}/${GID:-1000} for getter
  # so host-mounted $HOME is traversable.
  if ! ( cd "$REPO_ROOT" && env UID="$(id -u)" GID="$(id -g)" bash -c "$docker_runner compose up -d --build --wait" ) >/tmp/harporis-stack.log 2>&1; then
    cat /tmp/harporis-stack.log >&2
    warn "stack bring-up failed (see output above) — run \`make stack-up\` manually after fixing"
    return
  fi
  ok "stack healthy (4 containers: nats, getter, scanner, writer)"
}

doctor_check() {
  if ! command -v "$PREFIX/bin/harporis" >/dev/null 2>&1; then
    warn "harporis just installed but not on current shell PATH — open a new terminal"
    return
  fi
  log "running harporis doctor"
  "$PREFIX/bin/harporis" doctor || true
}

# ---- main -------------------------------------------------------------------

log "harporis installer"
log "PREFIX=$PREFIX  SHELL=$(detect_shell)  GO_VERSION=$GO_VERSION  SKIP_STACK=$SKIP_STACK"
ensure_go
ensure_docker
build_and_install
install_completion
bring_up_stack
doctor_check

cat <<EOF

${C_GREEN}✓ done.${C_RESET}

Next steps:
  ${C_DIM}# pick up updated rc / PATH:${C_RESET}
  exec \$SHELL

  ${C_DIM}# scan a repo on your host (auto-mounted via getter:/host):${C_RESET}
  harporis scan --local ~/path/to/your/repo

  ${C_DIM}# read the findings (NDJSON, one per line):${C_RESET}
  harporis findings list
  harporis findings show <scan_id>

  ${C_DIM}# tear down the stack when done:${C_RESET}
  cd $REPO_ROOT && make stack-down

Re-run the script any time — every step is idempotent.
EOF
