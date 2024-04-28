# Many thanks to original author Brandon Smith (onemorebsmith).
FROM golang:1.19.1 as builder

LABEL org.opencontainers.image.description="Dockerized Consensus Stratum Bridge"
LABEL org.opencontainers.image.authors="Consensus Community"
LABEL org.opencontainers.image.source="https://github.com/GRinvestPOOL/consensus-stratum-bridge"

WORKDIR /go/src/app
ADD go.mod .
ADD go.sum .
RUN go mod download

ADD . .
RUN go build -o /go/bin/app ./cmd/consensusbridge


FROM gcr.io/distroless/base:nonroot
COPY --from=builder /go/bin/app /
COPY cmd/consensusbridge/config.yaml /

WORKDIR /
ENTRYPOINT ["/app"]
