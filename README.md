# Mina performance testing tools

The Mina network performance testing tools

## Quick start

```shell
git submodule sync && git submodule update --recursive --init
```

## Public docker images

[https://hub.docker.com/r/o1labs/mina-perf-testing/tags](https://hub.docker.com/r/o1labs/mina-perf-testing/tags)

## Components

### Performance testing scenarios generator and orchestrator executables

- [./orchestrator](./orchestrator)

#### Build and publish

```shell
cd orchestrator
docker build -t mina-perf-testing-orchestrator .
docker tag mina-perf-testing-orchestrator o1labs/mina-perf-testing:orchestrator
docker push o1labs/mina-perf-testing:orchestrator
```

Example container start-up command:

```shell
docker run -id \
  --env GENERATOR_CLI_ARGS="" \
  --volume $(pwd)/orchestrator-config.json:/orchestrator-config.json \
  --volume $(pwd)/private-keys:/private-keys \
  --volume $(pwd)/experiment.script:/experiment.script \
  o1labs/mina-perf-testing:orchestrator
```

The `/orchestrator-config.json`, `/private-keys` attached volumes and either `/experiment.script` attached volume or `GENERATOR_CLI_ARGS` environment variable are required for the container's successful start-up.  
The `/experiment.script` attached volume takes precedence over the `GENERATOR_CLI_ARGS` environment variable that in other case will be used as the CLI arguments passed to the `generator` executable to generate the experiment script before launching the `orchestrator` executable.

Where `/private-keys` should be the directory with the private keys file(s) that are going to be used during the experiment.

You can find an example of the `orchestrator-config.json` file in the [./orchestrator/scripts/example-orchestrator-config.json](./orchestrator/scripts/example-orchestrator-config.json) file.
And `GENERATOR_CLI_ARGS` environment variable example is the following:

```shell
-base-tps 0.80 -max-cost -experiment-name ci-experiment -payment-receiver B62qnKweK4BVxG7TA1VzhNr6GcTejXbrN6ycEQiW4ZgUCxHuWTQta4i -rounds 5 -round-stops 0 -stress-tps 0.80 -zkapp-ratio 0.90 -generate-privkeys 20 -privkeys-per-fund 2 -zkapp-soft-limit -1 -pause 30 private-keys/bp1
```

### Logs fetcher and consumer executables

- [./internal-trace-consumer](./internal-trace-consumer)
  - Git submodule.

#### Build and publish

```shell
cd internal-trace-consumer
docker build -t mina-internal-trace-consumer .
docker tag mina-internal-trace-consumer o1labs/mina-perf-testing:internal-trace-consumer
docker push o1labs/mina-perf-testing:internal-trace-consumer
```

### Logs fetcher and consumer infrastructure configuration

- [./fetcher-infra-tmp](./fetcher-infra-tmp)
  - Temporary solution before it will be merged with the standard K8S deployment.

### Dashboard

The dashboard for the target network monitoring and performance analysis.

- [./mina-frontend](./mina-frontend)
  - Git submodule.

#### Build and publish

```shell
cd mina-frontend
docker build \
  --build-arg FETCHER_HOST="http://localhost" \
  --build-arg FETCHER_PORT="4000" \
  --build-arg APP_CONFIG="fetcher" \
  --build-arg EXPERIMENTS_BACKEND_API_ENDPOINT="http://localhost:3003/api/experiments" \
  -t mina-frontend-<ENV_NAME> .
docker tag mina-frontend-<ENV_NAME> o1labs/mina-perf-testing:dashboard-<ENV_NAME>
docker push o1labs/mina-perf-testing:dashboard-<ENV_NAME>
```

Where `<ENV_NAME>` is the environment name the dashboard was built for (considering the Docker image build arguments passed).  
This is required because the dashboard building procedure results in 100% client-side application, which is not aware of the environment it is running in.  
Also please make sure that the `FETCHER_HOST` and `FETCHER_PORT` are publicly available (same reason as it was described above, the client-side application).

### Experiments API

The API backend for `mina-frontend` to fetch experiments data from DB.

- [./experiments-api](./experiments-api)

#### Build and publish

```shell
cd experiments-api
docker build -t mina-experiments-api-<ENV_NAME> .
docker tag mina-experiments-api-<ENV_NAME> o1labs/mina-perf-testing:experiments-api-<ENV_NAME>
docker push o1labs/mina-perf-testing:experiments-api-<ENV_NAME>
```

Example container start-up command:

```shell
docker run -id -p 3003:3003 --env PSQL_CONNECTION_STRING="postgresql://postgres:postgres@localhost:5432/db" o1labs/mina-perf-testing:experiments-api-<ENV_NAME>
```

Where `<ENV_NAME>` is the environment name the backend is going to be used within.
Also please make sure that the `PSQL_CONNECTION_STRING` is provided during the container startup procedure.

### In-memory uptime backend

- [./uptime-backend](./uptime-backend)

#### Build and publish

```shell
cd uptime-backend
make clean build
# Or
make clean docker-publish
```

#### Docker container notes

Please make sure to also provide the corresponding application configuration file and environment variables during the container start-up.

Example configuration file:

```json
{
  "in_memory": true,
  "whitelist": [
    "B62qkasW9RRENzCzdEov1PRQ63BUT2VQK9iU7imcvbPLThnhL2eYMz8",
    "B62qp3x5osG6Fz6j44FVn61E4DNpAnyDEMcoQdNQZAdhaR7sj4wZ6gW",
    "B62qii4xfjQ3Vg5dsq7RakYTENQkdD8pFPMgqtUdC9FhgvBbwEbRoML"
  ]
}
```

Example container start-up command:

```shell
docker run \
  -id \
  -p 8080:8080 \
  -v ./config.json:/app/config.json \
  -e CONFIG_FILE="/app/config.json" \
  -e NETWORK="testnet" \
  o1labs/mina-perf-testing:in-memory-uptime-backend
```

Reported network participants then can be found at [http://localhost:8080/v1/online](http://localhost:8080/v1/online).
