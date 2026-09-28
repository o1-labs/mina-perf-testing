#!/usr/bin/env bash
# Generates everything the local network needs under $OUT (default /out).
#
# It runs inside the Mina daemon image (see the Makefile), which already has
# mina, openssl and jq, so the host needs nothing but docker.
#
#   local-env.sh keys     create the keys once; a second run keeps them
#   local-env.sh genesis  write a runtime config and orchestrator config with
#                         a genesis timestamp GENESIS_DELAY_SEC from now
set -euo pipefail

OUT=${OUT:-/out}
KEYS=$OUT/keys
PASS=${MINA_PRIVKEY_PASS:?MINA_PRIVKEY_PASS must be set}
SLOT_MS=${SLOT_MS:-20000}
GENESIS_DELAY_SEC=${GENESIS_DELAY_SEC:-180}

# Every daemon also sends uptime submissions signed with its own key; the
# block producers use their block producer key for both.
NODES="seed bp-1 bp-2 plain-1 plain-2"

mina_keypair() {
  local name=$1
  [ -f "$KEYS/$name" ] && return
  MINA_PRIVKEY_PASS=$PASS mina advanced generate-keypair --privkey-path "$KEYS/$name" >/dev/null
  chmod 600 "$KEYS/$name"
}

# An ITN key is a base64 ed25519 seed; the node's --itn-keys takes the base64
# public key. Both the orchestrator and the trace fetcher sign with one.
itn_keypair() {
  local name=$1
  [ -f "$KEYS/$name" ] && return
  local pem
  pem=$(openssl genpkey -algorithm ed25519)
  printf '%s' "$pem" | openssl pkey -outform DER | tail -c 32 | base64 >"$KEYS/$name"
  printf '%s' "$pem" | openssl pkey -pubout -outform DER | tail -c 32 | base64 >"$KEYS/$name.pub"
  chmod 600 "$KEYS/$name"
}

cmd_keys() {
  mkdir -p "$KEYS"
  chmod 700 "$KEYS"
  for n in $NODES whale; do mina_keypair "$n"; done
  itn_keypair orchestrator_sk
  itn_keypair fetcher_sk
  if [ ! -f "$KEYS/seed-libp2p" ]; then
    # The seed has a fixed libp2p identity so the other nodes can name it
    # in --peer. mkdtemp needs a writable working directory.
    (cd /tmp && MINA_LIBP2P_PASS=$PASS mina libp2p generate-keypair \
      --privkey-path "$KEYS/seed-libp2p" >/dev/null)
    chmod 600 "$KEYS/seed-libp2p"
  fi
  echo "keys ready in $KEYS"
}

pub() { cat "$KEYS/$1.pub"; }

cmd_genesis() {
  [ -f "$KEYS/whale.pub" ] || { echo "run 'local-env.sh keys' first" >&2; exit 1; }
  local ts
  ts=$(date -u -d "+${GENESIS_DELAY_SEC} seconds" +%Y-%m-%dT%H:%M:%SZ)

  # proof.level none: a Full-compiled daemon accepts it at run time, and it
  # keeps five daemons within a laptop's memory. The ledger gives each block
  # producer its own stake and delegates the whale to bp-1, so both win slots.
  jq -n \
    --arg ts "$ts" --argjson slot "$SLOT_MS" \
    --arg bp1 "$(pub bp-1)" --arg bp2 "$(pub bp-2)" \
    --arg whale "$(pub whale)" --arg seed "$(pub seed)" '
    { genesis: { genesis_state_timestamp: $ts },
      proof: { level: "none", block_window_duration_ms: $slot },
      ledger: {
        name: "perf-local",
        add_genesis_winner: false,
        accounts: [
          { pk: $bp1,   balance: "5000000",   delegate: null },
          { pk: $bp2,   balance: "5000000",   delegate: null },
          { pk: $whale, balance: "100000000", delegate: $bp1 },
          { pk: $seed,  balance: "1000",      delegate: null }
        ] } }' >"$OUT/runtime-config.json"

  # The orchestrator must use the same genesis timestamp and slot length as
  # the daemons. It reaches the nodes by the addresses the uptime backend
  # records, which are container IPs on the compose network.
  jq -n \
    --arg key "$(cat "$KEYS/orchestrator_sk")" --arg ts "$ts" --argjson slot "$SLOT_MS" '
    { key: $key,
      slotDurationMs: $slot,
      genesisTimestamp: $ts,
      onlineURL: "http://uptime-backend:8080/v1/online",
      fundDaemonPorts: ["seed:8301"],
      minaExec: "mina",
      logLevel: "info" }' >"$OUT/orchestrator-config.json"
  chmod 600 "$OUT/orchestrator-config.json"

  local wl=()
  for n in $NODES; do wl+=("$(pub "$n")"); done
  printf '%s\n' "${wl[@]}" | jq -R . | jq -s '{ in_memory: true, whitelist: . }' \
    >"$OUT/uptime-backend.json"

  # Values docker compose substitutes into the node commands.
  cat >"$OUT/nodes.env" <<EOF
ITN_KEYS=$(cat "$KEYS/orchestrator_sk.pub"),$(cat "$KEYS/fetcher_sk.pub")
SEED_PEER_ID=$(cat "$KEYS/seed-libp2p.peerid")
SEED_PK=$(pub seed)
WHALE_PK=$(pub whale)
GENESIS_TIMESTAMP=$ts
EOF
  echo "genesis at $ts"
}

case "${1:-}" in
  keys) cmd_keys ;;
  genesis) cmd_genesis ;;
  *) echo "usage: $0 keys|genesis" >&2; exit 2 ;;
esac
