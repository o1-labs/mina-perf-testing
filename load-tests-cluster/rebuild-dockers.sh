#!/bin/bash

# This script rebuilds the Docker images for all services.

set -ex

TAG="2.0.0"


cd ../orchestrator
docker build . -t o1labs/mina-perf-testing:orchestrator-as-service-$TAG -f Dockerfile-service

cd ../mina-frontend
docker build . -t o1labs/mina-perf-testing:dashboard-$TAG -f Dockerfile

cd ../experiments-api
docker build . -t o1labs/mina-perf-testing:experiments-api-$TAG -f Dockerfile

cd ../internal-trace-consumer
docker build . -t o1labs/mina-perf-testing:internal-trace-consumer-$TAG -f Dockerfile

if [ "$PUSH" = "true" ]; then
    echo "Pushing images..."
    docker push o1labs/mina-perf-testing:orchestrator-as-service-$TAG
    docker push o1labs/mina-perf-testing:dashboard-$TAG
    docker push o1labs/mina-perf-testing:experiments-api-$TAG
    docker push o1labs/mina-perf-testing:internal-trace-consumer-$TAG
fi