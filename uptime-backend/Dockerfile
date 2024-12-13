FROM golang:1.23.4-bookworm AS builder
WORKDIR /
COPY . .
RUN make clean build

FROM debian:bookworm AS app
COPY --from=builder /result /result
ENV LD_LIBRARY_PATH="/result"
EXPOSE 8080

CMD ["/result/bin/uptime_backend"]
