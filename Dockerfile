FROM golang:1.24.5-alpine3.22 AS builder

RUN apk add --no-cache git gcc musl-dev ca-certificates

WORKDIR /app

ARG VERSION=dev
ARG COMMIT=none

COPY src/go.mod src/go.sum /app/
RUN go mod download

COPY src/ /app/
RUN go build -o /app/gocert -ldflags="-X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -w -s" .


FROM alpine:3.22.1

RUN apk add --no-cache curl socat openssl ca-certificates

RUN curl https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh | sh -s -- --install-online --nocron --home /root/.acme.sh --config-home /var/gocert/acme.sh/
ENV PATH="/root/.acme.sh:${PATH}"

COPY --from=builder /app/gocert /usr/local/bin/gocert

RUN mkdir -p /var/gocert/certs /config

VOLUME ["/var/gocert", "/root/.acme.sh", "/config"]
WORKDIR /var/gocert

ENTRYPOINT ["/usr/local/bin/gocert"]
CMD ["run", "/config/certs.yaml"]
