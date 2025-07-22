FROM golang:1.24.5-bookworm AS builder

WORKDIR /app

ARG VERSION=dev
ARG COMMIT=none

RUN set -ex \
    && apt update \
    && apt install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

COPY src/go.mod src/go.sum /app/
RUN go mod download

COPY src/ /app/

RUN go build -o /app/gocert -ldflags="-X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -w -s" .


FROM debian:bookworm-20250721

RUN set -ex \
    && apt update \
    && apt install -y --no-install-recommends curl socat openssl ca-certificates \
    && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

RUN curl https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh | sh -s -- --install-online --nocron --home /root/.acme.sh --config-home /var/gocert/acme.sh/
ENV PATH="/root/.acme.sh:${PATH}"

COPY --from=builder /app/gocert /usr/local/bin/gocert

RUN mkdir -p /var/gocert/certs /config

VOLUME ["/var/gocert", "/root/.acme.sh", "/config"]
WORKDIR /var/gocert

ENTRYPOINT ["/usr/local/bin/gocert"]
CMD ["run", "/config/certs.yaml"]
