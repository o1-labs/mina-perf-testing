# Mina performance testing tools

The Mina network performance testing tools

## Quick start

```shell
git submodule sync && git submodule update --recursive --init
```

## Public docker images

[https://hub.docker.com/r/o1labs/mina/tags](https://hub.docker.com/r/o1labs/mina/tags)

## Components

### Performance testing scenarios generator and orchestrator executables

- [./orchestrator](./orchestrator)

#### Build and publish

```shell
cd orchestrator
docker build -t mina-perf-testing-orchestrator .
docker tag mina-perf-testing-orchestrator o1labs/mina:perf-testing-orchestrator
docker push o1labs/mina:perf-testing-orchestrator
```

### Logs fetcher and consumer executables

- [./internal-trace-consumer](./internal-trace-consumer)
  - Git submodule.

#### Build and publish

```shell
cd internal-trace-consumer
docker build -t mina-internal-trace-consumer .
docker tag mina-internal-trace-consumer o1labs/mina:perf-testing-internal-trace-consumer
docker push o1labs/mina:perf-testing-internal-trace-consumer
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
docker build --build-arg FETCHER_HOST="http://localhost" --build-arg FETCHER_PORT="4000" --build-arg APP_CONFIG="fetcher" -t mina-frontend-<ENV_NAME> .
docker tag mina-frontend-<ENV_NAME> o1labs/mina:perf-testing-<ENV_NAME>-dashboard
docker push o1labs/mina:perf-testing-<ENV_NAME>-dashboard
```

Where `<ENV_NAME>` is the environment name the dashboard was built for (considering the Docker image build arguments passed).  
This is required because the dashboard building procedure results in 100% client-side application, which is not aware of the environment it is running in.
Also please make sure that the `FETCHER_HOST` and `FETCHER_PORT` are publicly available (same reason as it was described above, the client-side application).

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
docker run -id -p 8080:8080 -v ./config.json:/app/config.json -e CONFIG_FILE="/app/config.json" -e NETWORK="testnet" o1labs/mina:in-memory-uptime-backend
```

Reported network participants then can be found at [http://localhost:8080/v1/online](http://localhost:8080/v1/online).
