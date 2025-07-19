FROM golang:1.23.4-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git
COPY . .
RUN go get -d -v ./...
RUN CGO_ENABLED=0 go build -o /gocert -ldflags="-w -s" .


FROM alpine:latest

RUN apk add --no-cache curl socat openssl

RUN curl https://get.acme.sh | sh -s -- --home /root/.acme.sh
ENV PATH="/root/.acme.sh:${PATH}"

COPY --from=builder /gocert /usr/local/bin/gocert

RUN mkdir -p /var/gocert/certs /config

VOLUME ["/var/gocert", "/config", "/root/.acme.sh"]

ENTRYPOINT ["/usr/local/bin/gocert"]

CMD ["run", "/config/certs.yaml"]
