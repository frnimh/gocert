FROM golang:1.23.4-alpine AS builder

RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

COPY . .
RUN go get -d -v ./...
RUN go build -o /gocert -ldflags="-w -s" .


FROM alpine:3.22.1

RUN apk add --no-cache curl socat openssl

RUN curl https://get.acme.sh | sh -s -- --install-online --nocron --home /var/gocert/acme.sh/
ENV PATH="/root/.acme.sh:${PATH}"

COPY --from=builder /gocert /usr/local/bin/gocert

RUN mkdir -p /var/gocert/certs /config

VOLUME ["/var/gocert"]

ENTRYPOINT ["/usr/local/bin/gocert"]

CMD ["run", "/config/certs.yaml"]
