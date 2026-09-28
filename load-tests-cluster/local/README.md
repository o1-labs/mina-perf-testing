# Local perf-testing environment

This directory runs a private Mina network and the whole perf-testing stack on
one machine: the orchestrator service, the uptime backend, Postgres, the trace
fetcher, the log API, the experiments API and the dashboard. Use it to run an
experiment end to end without a cluster.

## Requirements

- Docker with Compose v2, and GNU make. The host needs nothing else: the
  scripts run inside the Mina image.
- About 12 GB of free memory. Each of the 5 daemons uses 1–2 GB.
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

## The network

| Node | Role |
|:--|:--|
| `seed` | libp2p seed and the only snark worker |
| `bp-1`, `bp-2` | block producers; `bp-1` also has the whale's stake |
| `plain-1`, `plain-2` | nodes without a role, which receive load |

All nodes use `minaprotocol/mina-daemon:4.0.0-6965b50-jammy-devnet`
(`MINA_IMAGE`). They run with `proof.level: none` and 20 s slots, from a
genesis ledger that `scripts/local-env.sh` writes into `generated/`.

Each node starts with `ITN_FEATURES=1`, `--itn-keys` (the orchestrator and
fetcher keys), `--itn-graphql-port 3086` and `--uptime-url`. That is the
interface the orchestrator uses on a real cluster: it discovers nodes through
the uptime backend's `/v1/online` and controls them over signed ITN GraphQL.
It funds its keys from the `whale` account with `mina advanced
itn-create-accounts` through the seed's client port.

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
| `initdb/000-init.sh` | applies `../init-sql`, then records the local deployment |
| `generated/` | keys and configs (git-ignored; `make clean` deletes it) |
