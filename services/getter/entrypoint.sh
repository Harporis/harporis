#!/bin/sh
# The compose file runs getter as the host UID:GID (user: ${UID}:${GID})
# so the read-only $HOME bind-mount stays traversable for --local scans.
# That arbitrary UID usually has no /etc/passwd entry, and OpenSSH's
# getpwuid() then aborts with "No user exists for uid N" — breaking
# ssh:// remote clones before the handshake even starts.
#
# Synthesize a passwd entry for the current UID if one is missing, and
# point HOME at a writable dir. /etc/passwd is made world-writable in the
# Dockerfile precisely so this works for any UID compose injects.
set -e

uid="$(id -u)"
if ! getent passwd "$uid" >/dev/null 2>&1; then
    echo "harporis:x:${uid}:$(id -g):harporis:/tmp:/sbin/nologin" >> /etc/passwd 2>/dev/null || true
fi
export HOME=/tmp

exec /usr/local/bin/getter "$@"
