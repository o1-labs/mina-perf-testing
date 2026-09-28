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

NODES="seed bp-1 bp-2 plain-1 plain-2"
ORCH=http://orchestrator-service:9090/api/v0/experiment
ONLINE=http://uptime-backend:8080/v1/online
# shellcheck disable=SC1091
. /out/nodes.env

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

cmd_status() {
  echo "genesis: $GENESIS_TIMESTAMP"
  for n in $NODES; do printf '%-8s height %s\n' "$n" "$(height "$n" || true)"; done
  echo "discovered by uptime backend: $(online_count) of 5"
  curl -sf -m 5 "$ORCH/status" | jq -c '.result | {name, status, step, step_name, errors}' 2>/dev/null ||
    echo "orchestrator: no experiment yet"
}

cmd_run() {
  wait_for "blocks (seed height >= 3)" 900 \
    bash -c "h=\$(bash \"$0\" _height seed); [ -n \"\$h\" ] && [ \"\$h\" -ge 3 ]"
  wait_for "all 5 nodes in /v1/online" 600 bash -c "[ \"\$(bash \"$0\" _online)\" -ge 5 ]"

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
  echo "PASS"
}

case "${1:-}" in
  status) cmd_status ;;
  run) cmd_run ;;
  _height) height "$2" ;;
  _online) online_count ;;
  *) echo "usage: $0 status|run" >&2; exit 2 ;;
esac
