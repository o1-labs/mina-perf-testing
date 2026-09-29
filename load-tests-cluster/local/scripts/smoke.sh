#!/usr/bin/env bash
# Runs on the compose network (the `tools` service), so it reaches every
# container by name.
#
#   smoke.sh status   print chain height per node, discovered nodes, and the
#                     orchestrator's current experiment
#   smoke.sh run      wait for the network, run one short experiment through
#                     the orchestrator service, and check that it ended
#                     without errors and that its transactions reached blocks
set -euo pipefail

ORCH=http://orchestrator-service:9090/api/v0/experiment
ONLINE=http://uptime-backend:8080/v1/online
ROSETTA=http://rosetta:3087
FETCHER=http://log-fetcher:4000
LOG_API=http://log-api:9080/graphql
# shellcheck disable=SC1091
. /out/nodes.env
# The nodes of the current topology (local-env.sh genesis).
NODES=$LOCAL_NODES
N_NODES=$(wc -w <<<"$NODES")

gql() { # node query
  curl -sf -m 5 -H 'Content-Type: application/json' \
    -d "$(jq -n --arg q "$2" '{query: $q}')" "http://$1:3085/graphql"
}

height() {
  gql "$1" '{ bestChain(maxLength: 1) { protocolState { consensusState { blockHeight } } } }' |
    jq -r '.data.bestChain[0].protocolState.consensusState.blockHeight // empty' 2>/dev/null || true
}

online_count() { curl -sf -m 5 "$ONLINE" | jq length 2>/dev/null || echo 0; }

# Counts user commands and zkApp commands in the seed's best chain that were
# sent by accounts other than the whale; the experiment's funded keys send
# them, so a non-zero count means the load reached blocks.
experiment_txns() {
  gql seed '{ bestChain(maxLength: 290) { transactions {
      userCommands { from } zkappCommands { zkappCommand { feePayer { body { publicKey } } } } } } }' |
    jq --arg w "$WHALE_PK" '[.data.bestChain[].transactions |
      (.userCommands[].from), (.zkappCommands[].zkappCommand.feePayer.body.publicKey)]
      | map(select(. != $w)) | length'
}

wait_for() { # description timeout_sec command...
  local what=$1 limit=$2 start=$SECONDS
  shift 2
  until "$@"; do
    if ((SECONDS - start > limit)); then
      echo "timed out after ${limit}s waiting for $what" >&2
      return 1
    fi
    sleep 5
  done
  echo "ok: $what ($((SECONDS - start))s)"
}

# --- trace pipeline: log-fetcher (mina-sdk ITN client) -> consumer -> log-api -

# The log-api name of each node the fetcher found: the last 8 characters of
# its submitter key, its IP and its ITN port (internal-log-fetcher node.rs).
fetcher_node_names() {
  curl -sf -m 5 "$FETCHER/nodes" |
    jq -r '.[] | "\(.submitter_pk[-8:])-\(.ip)-\(.graphql_port)"' 2>/dev/null || true
}

fetcher_node_count() { curl -sf -m 5 "$FETCHER/nodes" | jq length 2>/dev/null || echo 0; }

# Number of block traces the log-api has for a node.
node_traces() {
  curl -sf -m 10 -H 'Content-Type: application/json' \
    -d "$(jq -n --arg n "$1" '{query: "{ blockTraces(node_name: \"\($n)\", deployment_id: 1, maxLength: 20) }"}')" \
    "$LOG_API" | jq '.data.blockTraces.traces | length' 2>/dev/null || echo 0
}

# The fetcher reads each node's internal logs over ITN GraphQL; the consumer
# turns them into block traces in Postgres, which the log-api serves.
check_traces() {
  wait_for "the log fetcher found all $N_NODES nodes" 300 \
    bash -c "[ \"\$(bash \"$0\" _fetcher_nodes)\" -ge $N_NODES ]"
  local name
  for name in $(fetcher_node_names); do
    wait_for "block traces of $name in the log-api" 300 \
      bash -c "[ \"\$(bash \"$0\" _node_traces $name)\" -ge 1 ]"
  done
}

# --- archive and Rosetta (ARCHIVE=1 sets ARCHIVE_ADDRESS in nodes.env) -----

rosetta() { # endpoint json
  curl -sf -m 10 -H 'Content-Type: application/json' -d "$2" "$ROSETTA/$1"
}

network_id() { rosetta network/list '{}' | jq -c '.network_identifiers[0]'; }

archive_tip() {
  rosetta network/status "{\"network_identifier\": $(network_id)}" |
    jq -r '.current_block_identifier.index // empty' 2>/dev/null || true
}

# User commands (payments) of the experiment in the seed's best chain.
experiment_payments() {
  gql seed '{ bestChain(maxLength: 290) { transactions { userCommands { from } } } }' |
    jq --arg w "$WHALE_PK" '[.data.bestChain[].transactions.userCommands[].from
      | select(. != $w)] | length'
}

# The archive gets blocks from the seed, so its tip may be a block or two
# behind. It is also checked to hold the same block 2 as the seed, and at
# least as many user commands as the experiment put in the best chain.
# Rosetta's search/transactions counts user commands only, not zkApp
# commands.
check_archive() {
  local want seed_h
  want=$(experiment_payments)
  seed_h=$(height seed)
  seed_h=$(height seed)
  wait_for "archive tip within 2 blocks of the seed ($seed_h)" 180 \
    bash -c "t=\$(bash \"$0\" _archive_tip); [ -n \"\$t\" ] && [ \"\$t\" -ge $((seed_h - 2)) ]"

  local net a_hash s_hash
  net=$(network_id)
  a_hash=$(rosetta block "{\"network_identifier\": $net, \"block_identifier\": {\"index\": 2}}" |
    jq -r '.block.block_identifier.hash')
  s_hash=$(gql seed '{ block(height: 2) { stateHash } }' | jq -r '.data.block.stateHash')
  if [ -z "$a_hash" ] || [ "$a_hash" != "$s_hash" ]; then
    echo "FAIL: block 2 differs: archive '$a_hash', seed '$s_hash'" >&2
    return 1
  fi
  echo "ok: block 2 is the same in the archive and on the seed ($a_hash)"

  local total
  total=$(rosetta search/transactions "{\"network_identifier\": $net, \"limit\": 1}" |
    jq -r '.total_count')
  echo "user commands: $total in the archive (Rosetta search), $want of the experiment in the best chain"
  if [ "$total" -lt "$want" ]; then
    echo "FAIL: the archive holds fewer user commands than the best chain has from the experiment" >&2
    return 1
  fi
}

cmd_status() {
  echo "genesis: $GENESIS_TIMESTAMP"
  for n in $NODES; do printf '%-8s height %s\n' "$n" "$(height "$n" || true)"; done
  echo "topology: $LOCAL_TOPOLOGY"
  echo "discovered by uptime backend: $(online_count) of $N_NODES"
  curl -sf -m 5 "$ORCH/status" | jq -c '.result | {name, status, step, step_name, errors}' 2>/dev/null ||
    echo "orchestrator: no experiment yet"
  echo "nodes known to the log fetcher: $(fetcher_node_count) of $N_NODES"
  if [ -n "${ARCHIVE_ADDRESS:-}" ]; then
    echo "archive tip (Rosetta): $(archive_tip)"
  fi
}

cmd_run() {
  wait_for "blocks (seed height >= 3)" 900 \
    bash -c "h=\$(bash \"$0\" _height seed); [ -n \"\$h\" ] && [ \"\$h\" -ge 3 ]"
  wait_for "all $N_NODES nodes in /v1/online" 600 bash -c "[ \"\$(bash \"$0\" _online)\" -ge $N_NODES ]"

  local name
  name="local-smoke-$(date -u +%Y%m%d%H%M%S)"
  # One short round of payments and zkApps from 4 funded keys, no node stops.
  local req
  req=$(jq -n --arg name "$name" --arg whale "$WHALE_PK" '{
    experiment_name: $name,
    rounds: 1, round_duration_min: 3, pause_min: 1,
    stops_per_round: 0, max_stop_ratio: 0, gap: 30,
    base_tps: 0.3, stress_tps: 0.3, min_tps: 0.01, zkapp_ratio: 0.5,
    generate_fund_keys: 4, privkeys_per_fund_cmd: 1,
    priv_keys: ["/keys/whale"], password_env: "MINA_PRIVKEY_PASS",
    fund_key_prefix: "/tmp/fund_keys",
    payment_receiver: $whale }')

  echo "starting experiment $name"
  local resp
  resp=$(curl -s -m 30 -w '\n%{http_code}' -H 'Content-Type: application/json' -d "$req" "$ORCH/run")
  if [ "$(tail -n1 <<<"$resp")" != 200 ]; then
    echo "orchestrator refused the experiment: $(head -n -1 <<<"$resp")" >&2
    return 1
  fi

  local st="" start=$SECONDS last=""
  while :; do
    # The service wraps the experiment in {"result": ...}.
    st=$(curl -sf -m 5 "$ORCH/status" | jq '.result // empty' 2>/dev/null || true)
    local s step
    s=$(jq -r '.status // empty' <<<"$st" 2>/dev/null || true)
    step=$(jq -r '"\(.step) \(.step_name)"' <<<"$st" 2>/dev/null || true)
    [ "$step" != "$last" ] && echo "  [$((SECONDS - start))s] $s: step $step" && last=$step
    case "$s" in running | cancelling | "") ;; *) break ;; esac
    if ((SECONDS - start > 1800)); then
      echo "experiment did not finish in 30 min" >&2
      return 1
    fi
    sleep 10
  done

  jq '{name, status, errors, warnings}' <<<"$st"
  if [ "$(jq -r .status <<<"$st")" != success ] || [ "$(jq '.errors | length' <<<"$st")" != 0 ]; then
    echo "FAIL: experiment did not end cleanly" >&2
    return 1
  fi
  local n
  n=$(experiment_txns)
  echo "experiment transactions in the seed's best chain: $n"
  if [ "$n" -eq 0 ]; then
    echo "FAIL: no experiment transaction reached a block" >&2
    return 1
  fi
  check_traces
  if [ -n "${ARCHIVE_ADDRESS:-}" ]; then
    check_archive
  fi
  echo "PASS"
}

case "${1:-}" in
  status) cmd_status ;;
  run) cmd_run ;;
  _height) height "$2" ;;
  _online) online_count ;;
  _archive_tip) archive_tip ;;
  _fetcher_nodes) fetcher_node_count ;;
  _node_traces) node_traces "$2" ;;
  *) echo "usage: $0 status|run" >&2; exit 2 ;;
esac
