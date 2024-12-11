# Mina performance testing tools

The Mina network performance testing tools

## Quick start

```shell
git submodule sync && git submodule update --recursive --init
```

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
docker build --build-arg FETCHER_HOST="http://localhost" --build-arg FETCHER_PORT="4000" --build-arg APP_CONFIG="fetcher" -t mina-frontend .
docker tag mina-frontend o1labs/mina:perf-testing-<ENV_NAME>-dashboard
docker push o1labs/mina:perf-testing-<ENV_NAME>-dashboard
```

Where `<ENV_NAME>` is the environment name the dashboard was built for (considering the Docker image build arguments passed).  
This is required because the dashboard building procedure results in 100% client-side application, which is not aware of the environment it is running in.

### TODO: Uptime backend

[https://github.com/MinaFoundation/uptime-service-backend](https://github.com/MinaFoundation/uptime-service-backend)  
[https://github.com/MinaProtocol/mina/pull/15625](https://github.com/MinaProtocol/mina/pull/15625)
