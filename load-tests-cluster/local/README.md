# Local perf-testing environment

This directory runs a private Mina network and the whole perf-testing stack on
one machine: the orchestrator service, the uptime backend, Postgres, the trace
fetcher, the log API, the experiments API and the dashboard. Use it to run an
experiment end to end without a cluster.

## Requirements

- Docker with Compose v2, and GNU make. The host needs nothing else: the
  scripts run inside the Mina image.
- Free memory: about 14 GB for the full topology (5 daemons at up to about
  2.7 GB each during an experiment), about 6 GB for the minimal one.
- Docker Hub access for the base images. If a pull fails with
  `personal access token is expired`, log in again, or pull anonymously with
  `DOCKER_CONFIG=<a directory that holds {}> make up`.

## Use

```sh
make up       # generate keys (once) and a new genesis, build, start everything
make status   # chain height of each node, discovered nodes, experiment status
make smoke    # run one short experiment through the orchestrator and check it
make logs     # follow all container logs
make down     # stop; `make up` then starts a new chain
make clean    # also delete the Postgres data and the generated keys
```

`make up` starts the nodes in three waves (seed, block producers, plain
nodes) and sets genesis 180 s in the future. On a 16-core machine the first
blocks came about 3 minutes after `make up`, and all 5 nodes were in
`/v1/online` about 1 minute later. `make smoke` waits for both, and then
runs an experiment of about 10 minutes.

Endpoints (all on `127.0.0.1`; set the `LOCAL_*_PORT` variables to change them):

| Service | Port |
|:--|:--|
| Orchestrator service (`/api/v0/experiment/{run,status,cancel}`) | 9090 |
| Dashboard | 4200 |
| Experiments API | 3003 |
| Log API | 9080 |
| Uptime backend (`/v1/online`) | 8080 |
| Postgres (user `postgres`, password `local`, database `logs`) | 55433 |
| Seed node GraphQL | 3085 |
| Rosetta (`ARCHIVE=1`) | 3087 |
| Archive Postgres (`ARCHIVE=1`; user `postgres`, password `local`, database `archive`) | 55434 |

## The network

`TOPOLOGY` selects the nodes. Give the same value to every `make` command
(for example `make up smoke TOPOLOGY=minimal`), because `smoke` and `status`
read it too.

| Node | `TOPOLOGY=full` (default) | `TOPOLOGY=minimal` |
|:--|:--|:--|
| `seed` | libp2p seed and the only snark worker | seed, snark worker and the only block producer (with the whale's stake) |
| `bp-1`, `bp-2` | block producers; `bp-1` also has the whale's stake | not started |
| `plain-1` | node without a role, which receives load | node without a role, which receives load |
| `plain-2` | node without a role, which receives load | not started |

The minimal topology is for small machines and CI. On the machine it was
tested on, `make up` to `PASS` took about 10 minutes, and the two daemons used
about 2.6 GB each during the experiment. `bp-1`, `bp-2` and `plain-2` are in
the compose profile `full`; `make up` removes all nodes first, so a change of
topology does not leave nodes of the old chain running.

All nodes use `minaprotocol/mina-daemon:4.0.0-6965b50-jammy-devnet`
(`MINA_IMAGE`). They run with `proof.level: none` and 20 s slots, from a
genesis ledger that `scripts/local-env.sh` writes into `generated/`.

Each node starts with `ITN_FEATURES=1`, `--itn-keys` (the orchestrator and
fetcher keys), `--itn-graphql-port 3086` and `--uptime-url`. That is the
interface the orchestrator uses on a real cluster: it discovers nodes through
the uptime backend's `/v1/online` and controls them over signed ITN GraphQL.
It funds its keys from the `whale` account with `mina advanced
itn-create-accounts` through the seed's client port.

## Archive node and Rosetta

`ARCHIVE=1` adds three containers to either topology (compose profile
`archive`). Give it to every `make` command, as with `TOPOLOGY`:

```sh
make up smoke ARCHIVE=1
make up smoke TOPOLOGY=minimal ARCHIVE=1
```

| Container | Content |
|:--|:--|
| `archive-db` | Postgres for the archive only, separate from the perf-testing database |
| `archive` | `mina-archive run` (`MINA_ARCHIVE_IMAGE`, default `minaprotocol/mina-archive:4.0.0-6965b50-jammy-devnet`) with the nodes' runtime config. It applies `/etc/mina/archive/create_schema.sql` from its image to an empty database. |
| `rosetta` | `mina-rosetta` (`MINA_ROSETTA_IMAGE`, default `minaprotocol/mina-rosetta:4.0.0-6965b50-jammy-devnet`) on the archive database and the seed's GraphQL |

The seed sends every block it accepts to the archive (`--archive-address`),
and it starts only after the archive listens, so the archive has the chain
from block 1. The archive database has no named volume: `make up` removes it
with the nodes, because every `up` starts a new chain.

With `ARCHIVE=1`, `make smoke` also checks, through Rosetta, that the archive
tip is at most 2 blocks behind the seed, that block 2 has the same state hash
in the archive and on the seed, and that the archive holds at least as many
transactions as the experiment put in the best chain. `make status` shows the
archive tip.

Rosetta reports the network as `{"blockchain": "mina", "network": "testnet"}`
(`POST /network/list`). Its `search/transactions` counts user commands only;
zkApp commands are in the archive's `zkapp_commands` table.

There is no view of this data yet (for example `mina-frontend`); use Rosetta
or the archive database directly.

Details that the compose file handles:

- Rosetta exits at start without `MINA_ROSETTA_MAX_DB_POOL_SIZE` (set to 32).
- Rosetta's flags take their value as a separate argument: `--port 3087`,
  not `--port=3087`.
- The archive healthcheck reads `/proc/net/tcp` for the listening port. A TCP
  probe works too, but the archive logs each connection without an RPC
  handshake as an error.

## Constraints

- **Use a devnet-profile image.** `itn-create-accounts` signs with
  `Mina_signature_kind.Testnet`, which is fixed in its code. A mainnet-profile
  daemon rejects those transactions with `Invalid_signature`.
- **The image's network config is removed at start.** The daemon always reads
  `/var/lib/coda/config_<commit>.json` first, and a later config file cannot
  remove its `proof.fork` and `epoch_data`. If that file stays, the nodes
  stake from the real devnet ledger and win no slots.
  `scripts/node-entrypoint.sh` deletes it.
- **The uptime backend verifies testnet signatures** (`NETWORK=testnet`).
  With an empty `NETWORK` it uses the mainnet network ID and answers
  `Invalid signature` to devnet daemons, and no node is discovered.
- **The nodes start in waves.** A daemon exits (`Option.value_exn None` in
  `Uptime_service.start`) when its uptime SNARK worker does not connect within
  60 s. With five daemons starting at once this took about 60 s. A healthcheck
  on the worker's log line starts the next wave, and `restart: on-failure`
  is a second safety net.
- **The client port trusts the compose subnet only** (`10.77.0.0/24`, through
  `MINA_CLIENT_TRUSTLIST`; this build has no `--client-trustlist` flag). The
  orchestrator uses that port to fund keys.
- **The deployment record names the image's commit.** The orchestrator
  service accepts its bundled `mina` client only if the latest `deployment`
  row names the same commit (`initdb/000-init.sh`, `MINA_RELEASE`). If you
  change `MINA_IMAGE`, change `MINA_RELEASE` too and run `make clean`.
  Each experiment records the warning `Could not take the mina client from
  the deployed daemon image: ... 403 Forbidden ... Continuing`. This is
  expected: the service cannot read the private registry from a laptop, and
  it continues because the commits match.
- **Node restarts are not available.** Experiments with
  `use_restart_script` need a `ControlExec` for Docker, which does not exist
  yet.

## Files

| Path | Content |
|:--|:--|
| `docker-compose.yaml` | all services |
| `scripts/local-env.sh` | key generation, runtime config, orchestrator config |
| `scripts/node-entrypoint.sh` | daemon flags for each role |
| `scripts/smoke.sh` | `status` and `run` for `make status` and `make smoke` |
| `scripts/archive-entrypoint.sh` | archive schema on an empty database, then `mina-archive run` |
| `initdb/000-init.sh` | applies `../init-sql`, then records the local deployment |
| `generated/` | keys and configs (git-ignored; `make clean` deletes it) |
