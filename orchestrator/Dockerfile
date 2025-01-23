FROM golang:1.23.4-bookworm AS builder
WORKDIR /
COPY . .
RUN make generator && make orchestrator

FROM debian:bookworm AS app
COPY --from=builder /generator /generator
COPY --from=builder /orchestrator /orchestrator
COPY --from=builder /scripts/entrypoint.sh /entrypoint.sh

CMD [ "bash", "/entrypoint.sh" ]
