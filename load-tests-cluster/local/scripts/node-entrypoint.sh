#!/usr/bin/env bash
# Starts one daemon of the local network.
#
# NODE_NAME names the node's key in /keys, which also signs its uptime
# submissions. NODE_ROLE is seed, bp or plain. The remaining values come from
# generated/nodes.env (see local-env.sh genesis). MINA_CLIENT_TRUSTLIST
# (CIDRs) opens the client port (8301) to the orchestrator, which funds
# keys through it; this build has no --client-trustlist flag.
set -euo pipefail

: "${NODE_NAME:?}" "${NODE_ROLE:?}" "${ITN_KEYS:?}" "${MINA_CLIENT_TRUSTLIST:?}" "${SEED_PEER_ID:?}" "${SEED_PK:?}"

# The image installs its network's config (config_<commit>.json) and the
# daemon always reads it first. A later config file cannot remove keys from
# it, so its proof.fork and epoch_data would stay and the node would stake
# from the real devnet ledger, where our keys own nothing: no slots are won.
rm -f /var/lib/coda/config_*.json

args=()
case "$NODE_ROLE" in
  seed)
    # The seed also runs the only snark worker. With proof level none the
    # work is cheap, but without a worker the scan state fills and blocks
    # stop including transactions.
    args+=(--seed --libp2p-keypair /keys/seed-libp2p
      --run-snark-worker "$SEED_PK" --snark-worker-fee 0.001)
    # In the minimal topology the seed is also the only block producer.
    if [ "${SEED_BLOCK_PRODUCER:-0}" = 1 ]; then
      args+=(--block-producer-key /keys/seed)
    fi
    # With ARCHIVE=1 the seed sends every block it accepts to the archive.
    if [ -n "${ARCHIVE_ADDRESS:-}" ]; then
      args+=(--archive-address "$ARCHIVE_ADDRESS")
    fi
    ;;
  bp)
    args+=(--block-producer-key "/keys/$NODE_NAME")
    ;;
  plain) ;;
  *) echo "unknown NODE_ROLE=$NODE_ROLE" >&2; exit 2 ;;
esac
if [ "$NODE_ROLE" != seed ]; then
  args+=(--peer "/dns4/seed/tcp/8302/p2p/$SEED_PEER_ID")
fi

exec mina daemon \
  --config-file /local/runtime-config.json \
  --uptime-url http://uptime-backend:8080/v1/submit \
  --uptime-submitter-key "/keys/$NODE_NAME" \
  --itn-keys "$ITN_KEYS" \
  --itn-graphql-port 3086 \
  --insecure-rest-server \
  --internal-tracing \
  --log-level Info \
  --file-log-level Info \
  "${args[@]}" "$@"
