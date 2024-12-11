FROM golang:1.23.4-bookworm AS builder
WORKDIR /
COPY . .
RUN make generator && make orchestrator

FROM debian:bookworm AS app
COPY --from=builder /generator /generator
COPY --from=builder /orchestrator /orchestrator
COPY --from=builder /default-orchestrator-config.json /default-orchestrator-config.json
RUN ./generator \
  -base-tps 1 \
  -max-cost-mixed 0.7 \
  -experiment-name default-experiment \
  -payment-receiver B62qnKweK4BVxG7TA1VzhNr6GcTejXbrN6ycEQiW4ZgUCxHuWTQta4i \
  -rounds 2 \
  -round-stops 0 \
  -stress-tps 1 \
  -zkapp-ratio 0.9 \
  keys/bp1 > default-experiment.script

CMD [ "./orchestrator", "default-orchestrator-config.json", "<default-experiment.script" ]
