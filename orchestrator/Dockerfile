FROM golang:1.23.4-bookworm AS builder
WORKDIR /
# The build context carries .git, and the builder runs as a different user than
# the one that owns it, so `go build` fails with
# "error obtaining VCS status: exit status 128" unless stamping is switched off.
ENV GOFLAGS=-buildvcs=false
COPY . .
RUN make generator && make orchestrator

FROM debian:bookworm AS app
COPY --from=builder /generator /generator
COPY --from=builder /orchestrator /orchestrator
COPY --from=builder /scripts/entrypoint.sh /entrypoint.sh

CMD [ "bash", "/entrypoint.sh" ]
