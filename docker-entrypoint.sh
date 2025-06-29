#!/bin/sh
# /usr/local/bin/docker-entrypoint.sh
set -e

ID="${NODE_ID:-0}"                     # fall back to 0 for safety
PEERS="${PEERS_FILE:-/etc/epaxos/peers.txt}"

exec /usr/local/bin/epaxos \
     --id="$ID" \
     --peersFile="$PEERS" \
     "$@"
