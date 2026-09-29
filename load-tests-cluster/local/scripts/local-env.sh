#!/usr/bin/env bash
# Generates everything the local network needs under $OUT (default /out).
#
# It runs inside the Mina daemon image (see the Makefile), which already has
# mina, openssl and jq, so the host needs nothing but docker.
#
#   local-env.sh keys     create the keys once; a second run keeps them
#   local-env.sh genesis  write a runtime config and orchestrator config with
#                         a genesis timestamp GENESIS_DELAY_SEC from now, for
#                         TOPOLOGY full (5 nodes) or minimal (2 nodes), and
#                         with ARCHIVE=1 an archive node the seed sends to
set -euo pipefail

OUT=${OUT:-/out}
KEYS=$OUT/keys
PASS=${MINA_PRIVKEY_PASS:?MINA_PRIVKEY_PASS must be set}
SLOT_MS=${SLOT_MS:-20000}
GENESIS_DELAY_SEC=${GENESIS_DELAY_SEC:-180}
TOPOLOGY=${TOPOLOGY:-full}
ARCHIVE=${ARCHIVE:-0}

# Every daemon also sends uptime submissions signed with its own key; the
# block producers use their block producer key for both. Keys are made for
# all nodes, so a change of topology needs no new keys.
ALL_NODES="seed bp-1 bp-2 plain-1 plain-2"

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
  for n in $ALL_NODES whale; do mina_keypair "$n"; done
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
  local ts nodes seed_bp accounts
  ts=$(date -u -d "+${GENESIS_DELAY_SEC} seconds" +%Y-%m-%dT%H:%M:%SZ)

  # full: bp-1 and bp-2 each have their own stake, and the whale delegates to
  # bp-1, so both win slots. minimal: the seed is also the only block
  # producer, and the whale delegates to it.
  case "$TOPOLOGY" in
    full)
      nodes=$ALL_NODES
      seed_bp=0
      accounts=$(jq -n --arg bp1 "$(pub bp-1)" --arg bp2 "$(pub bp-2)" \
        --arg whale "$(pub whale)" --arg seed "$(pub seed)" '[
          { pk: $bp1,   balance: "5000000",   delegate: null },
          { pk: $bp2,   balance: "5000000",   delegate: null },
          { pk: $whale, balance: "100000000", delegate: $bp1 },
          { pk: $seed,  balance: "1000",      delegate: null } ]')
      ;;
    minimal)
      nodes="seed plain-1"
      seed_bp=1
      accounts=$(jq -n --arg whale "$(pub whale)" --arg seed "$(pub seed)" '[
          { pk: $seed,  balance: "5000000",   delegate: null },
          { pk: $whale, balance: "100000000", delegate: $seed } ]')
      ;;
    *)
      echo "TOPOLOGY must be full or minimal, not '$TOPOLOGY'" >&2
      exit 2
      ;;
  esac

  # proof.level none: a Full-compiled daemon accepts it at run time, and it
  # keeps several daemons within a laptop's memory.
  jq -n --arg ts "$ts" --argjson slot "$SLOT_MS" --argjson accounts "$accounts" '
    { genesis: { genesis_state_timestamp: $ts },
      proof: { level: "none", block_window_duration_ms: $slot },
      ledger: { name: "perf-local", add_genesis_winner: false, accounts: $accounts } }' \
    >"$OUT/runtime-config.json"

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
  for n in $nodes; do wl+=("$(pub "$n")"); done
  printf '%s\n' "${wl[@]}" | jq -R . | jq -s '{ in_memory: true, whitelist: . }' \
    >"$OUT/uptime-backend.json"

  # Values the node containers and smoke.sh read at run time.
  cat >"$OUT/nodes.env" <<EOF
ITN_KEYS=$(cat "$KEYS/orchestrator_sk.pub"),$(cat "$KEYS/fetcher_sk.pub")
SEED_PEER_ID=$(cat "$KEYS/seed-libp2p.peerid")
SEED_PK=$(pub seed)
WHALE_PK=$(pub whale)
GENESIS_TIMESTAMP=$ts
LOCAL_TOPOLOGY=$TOPOLOGY
LOCAL_NODES="$nodes"
SEED_BLOCK_PRODUCER=$seed_bp
ARCHIVE_ADDRESS=$([ "$ARCHIVE" = 1 ] && echo archive:3086)
EOF
  echo "genesis at $ts, topology $TOPOLOGY: $nodes, archive: $ARCHIVE"
}

case "${1:-}" in
  keys) cmd_keys ;;
  genesis) cmd_genesis ;;
  *) echo "usage: $0 keys|genesis" >&2; exit 2 ;;
esac
