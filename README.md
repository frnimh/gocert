# GoCert Manager

GoCert Manager is a lightweight, stateful daemon that automates the issuance and renewal of TLS certificates. It leverages the power and broad DNS provider support of acme.sh while adding a persistent SQLite database for state management and a simple YAML-based configuration. It is designed to run continuously as a containerized service, ensuring your certificates never expire.

## Key Features

- **Automated Renewals:** Runs as a persistent daemon, periodically checking certificate status and automatically renewing them before they expire.
- **Stateful Management:** Uses an SQLite database to track certificate status, issuance dates, and renewal history.
- **Simple YAML Configuration:** Manage all your certificates from a single, human-readable YAML file.
- **Broad DNS Provider Support:** Powered by acme.sh, it supports over 100 DNS providers for handling DNS-01 challenges, which is required for wildcard certificates.
- **Concurrent Processing:** Checks multiple certificates in parallel using goroutines for efficient processing.
- **Container-First Design:** Includes a multi-stage Dockerfile for creating a small, secure, and portable runtime image.
- **Easy Status Checks:** A simple CLI command lets you view the status, provider, and expiry date of all managed certificates.

## How It Works

GoCert Manager simplifies the certificate lifecycle into a hands-free, automated process:

    Run as a Daemon: The application starts and runs continuously, either as a system service or a Docker container.

    Load Configuration: On startup and at a regular interval (default: every hour), it reads the certs.yaml file to get the desired state of your certificates.

    Check State: For each certificate in the config, it queries its internal SQLite database to see its current status and last issuance date.

    Determine Action: It calculates if a certificate needs action:

        New Certificate: If a certificate is in the config but not the database, it triggers an issuance.

        Renewal: If a certificate is due to expire within a set threshold (default: 10 days), it triggers a renewal.

    Invoke acme.sh: It delegates the ACME challenge process to acme.sh, which handles the DNS-01 challenge with the specified provider.

    Store Artifacts: The new certificate files (fullchain.pem, key.pem, etc.) are saved to a persistent volume.

    Update Database: Upon success or failure, it updates the certificate's record in the database with the new status (issued or failed) and the new issuance timestamp.

Getting Started

The recommended way to run GoCert Manager is with Docker.
Prerequisites

## Getting Started

The recommended way to run GoCert Manager is with Docker.

### Prerequisites

- Docker installed and running.
- API credentials for your DNS provider, which acme.sh will need.

### 1. Project Structure

Create a directory structure to hold your configuration and persistent data:

    config/: This directory will be mounted into the container to provide the configuration file.

    data/: This directory will be mounted to store the SQLite database and the issued certificates.

2. Create the Configuration File

Create your config/certs.yaml file. This file defines every certificate you want the service to manage.

# config/certs.yaml
### 2. Create the Configuration File

Create your `config/certs.yaml` file. This file defines every certificate you want the service to manage.
    - "example.com"
    - "*.example.com" # Wildcard requires DNS-01 challenge
  issuer: "zerossl"
  type: "dns_aws" # Corresponds to an acme.sh DNS provider

another-service:
  domains:
    - "service.example.org"
  issuer: "letsencrypt"
  type: "dns_cf" # Cloudflare provider

3. Configure acme.sh Credentials

acme.sh needs environment variables to authenticate with your DNS provider's API. You must provide these to the Docker container.

For example, for Cloudflare (dns_cf), you would need CF_Token and CF_Account_ID. Refer to the acme.sh DNS API documentation to find the required variables for your provider.
### 3. Configure acme.sh Credentials

acme.sh needs environment variables to authenticate with your DNS provider's API. You must provide these to the Docker container.

docker build -t gocert-manager .
### 4. Build and Run the Docker Container

First, build the Docker image from the project root where the Dockerfile is located:
# Replace with your actual DNS provider credentials
export CF_Token="your-cloudflare-api-token"
export CF_Account_ID="your-cloudflare-account-id"

docker run -d \
  --name gocert-daemon \
  --restart always \
  -e CF_Token="$CF_Token" \
  -e CF_Account_ID="$CF_Account_ID" \
  -v ./config:/config \
  -v ./data:/var/gocert \
  -v gocert-acme-data:/root/.acme.sh \
  gocert-manager

Explanation of the docker run command:

    -d: Runs the container in detached mode.

    --name gocert-daemon: Assigns a convenient name to the container.

    --restart always: Ensures the container restarts automatically if it stops.

    -e ...: Passes the required environment variables for acme.sh.

    -v ./config:/config: Mounts your configuration directory.

    -v ./data:/var/gocert: Mounts the data directory to persist the database and certificates.

    -v gocert-acme-data:/root/.acme.sh: Mounts a named volume to persist acme.sh's own internal configuration and account keys. This is crucial for avoiding ACME rate limits.

5. Checking Status and Logs

You can easily check on the status of your certificates or view the daemon's logs.

To view the status of all certificates:
### 5. Checking Status and Logs

You can easily check on the status of your certificates or view the daemon's logs.
Example Output:

NAME                  STATUS  ISSUED      EXPIRES     REMAINING   TLS PROVIDER    DNS PROVIDER
----                  ------  ------      -------     ---------   ------------    ------------
my-production-site    issued  2025-07-19  2025-10-17  90 days     zerossl         dns_aws
another-service       issued  2025-07-19  2025-10-17  90 days     letsencrypt     dns_cf

To follow the application logs in real-time:

docker logs -f gocert-daemon

License

This project is licensed under the MIT License. See the LICENSE file for details.
## License

This project is licensed under the MIT License. See the LICENSE file for details.
