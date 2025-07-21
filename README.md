# gocert

A tool for managing certificates and related services using Docker Compose.

## Setup

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [Docker Compose](https://docs.docker.com/compose/)
- `docker-compose.yaml` and `certs.yaml` files in the project directory

### Installation

1. **Clone the repository:**
  ```sh
  git clone <repo-url>
  cd gocert
  ```

2. **Place your domains and configs in `certs.yaml` and API keys as variables in `docker-compose.yaml` file.**

  ```yaml
  configs:
    email: my@example.com

  test:
    domains:
      - "example.com"
      - "*.example.com"
    issuer: "zerossl"
    type: "dns_aws"
  ```

  `email` for some CA Providers e.g: `zerossl` you need to set an Email Address.

  `domains` list the Domains that you want the Specific cert for, it cloud be wildcard Domains too.

  `issuer` is your TLS Provider (CA) shortname or URL, check out acme.sh docs for more information. [Link](https://github.com/acmesh-official/acme.sh/wiki/Server)

  `type` your DNS Provider API in acme.sh, checkout acme.sh docs for more information. [Link](https://github.com/acmesh-official/acme.sh/wiki/dnsapi)

  `dns_*` you need to set your keys as Variables in `docker-compose.yaml`, check sample compose file in this repo; and read acme.sh docs for more information. [Link](https://github.com/acmesh-official/acme.sh/wiki/dnsapi)


3. **Start the services:**
  ```sh
  docker-compose up -d
  ```

4. **Check running containers:**
  ```sh
  docker-compose ps
  ```

## Configuration

- **`docker-compose.yaml`**: Defines the services, networks, and volumes.
- **`certs.yaml`**: Contains certificate configuration (domains, issuer, etc).

## Checking Details

5. **Get more Details about your certs**

you can run `gocert status` to get more details about your certificates.

  ```
  NAME    STATUS   ISSUED       EXPIRES      REMAINING   TLS PROVIDER   DNS PROVIDER
  ----    ------   ------       -------      ---------   ------------   ------------
  test    issued   2025-07-19   2025-10-17   89 days     zerossl        dns_aws
  ```

---

# Technical Documentation

## Overview

`main.go` is the entry point for the gocert tool. It initializes the application, loads configuration, and starts the certificate management process.

## Main Features

- Loads configuration from `certs.yaml`
- Initializes Docker Compose services
- Manages certificate lifecycle (creation, renewal, etc)
- Provides logging for operations

## Usage

Run the tool:

```sh
go run main.go
```

Or build and run:

```sh
go build -o gocert
./gocert
```

## Main Components

- **Config Loader**: Reads and parses `certs.yaml`
- **Docker Manager**: Interfaces with Docker Compose to manage services
- **Certificate Handler**: Handles certificate operations

---

For more details, review the source code in `main.go`.
