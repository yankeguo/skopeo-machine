FROM golang:1.24 AS builder

ENV CGO_ENABLED=0

WORKDIR /go/src/app

ADD . .

RUN go build -o /skopeo-machine ./cmd/skopeo-machine

FROM alpine:3.21

RUN apk add --no-cache ca-certificates 

COPY --from=builder /skopeo-machine /skopeo-machine

ENTRYPOINT ["/skopeo-machine"]

WORKDIR /data