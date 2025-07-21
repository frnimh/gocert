# Stage 1: Build the Go application
FROM golang:1.23.4-alpine AS builder

# Install build tools required for CGO, including a C compiler (gcc) and musl-dev.
# Also install git for fetching Go modules.
RUN apk add --no-cache git gcc musl-dev

# Set the working directory inside the container.
WORKDIR /app

# Declare build arguments for versioning.
# These will be populated by the 'docker build' command.
ARG VERSION=dev
ARG COMMIT=none

# Copy the Go source code.
COPY . .

# Fetch dependencies.
RUN go get -d -v ./...

# Build the Go application, embedding the version and commit hash using ldflags.
RUN go build -o /gocert -ldflags="-X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -w -s" .


# Stage 2: Create the final, lightweight runtime image
FROM alpine:3.22.1

# Install runtime dependencies for acme.sh (curl, socat, openssl)
RUN apk add --no-cache curl socat openssl

# Install acme.sh using the official installer script.
# Note: The config home is now inside /var/gocert, which should be a volume.
RUN curl https://raw.githubusercontent.com/acmesh-official/acme.sh/master/acme.sh | sh -s -- --install-online --nocron --home /root/.acme.sh --config-home /var/gocert/acme.sh/
ENV PATH="/root/.acme.sh:${PATH}"

# Copy the compiled application binary from the builder stage.
COPY --from=builder /gocert /usr/local/bin/gocert

# Create directories for the application's data and configuration.
RUN mkdir -p /var/gocert/certs /config

# Define the volume for all persistent data.
# This single volume now holds the app's db, certs, and acme.sh config.
VOLUME ["/var/gocert"]

# Set the entrypoint to our application.
ENTRYPOINT ["/usr/local/bin/gocert"]

# Set the default command to run the daemon.
# The user must mount their configuration file to /config/certs.yaml for this to work.
CMD ["run", "/config/certs.yaml"]
