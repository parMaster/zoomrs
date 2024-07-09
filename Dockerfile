# Description: Dockerfile to build Zoomrs Service
# Author: Gusto (https://github.com/parmaster)
# Usage: 
#	docker build -t zoomrs .
#	docker run -it --rm -p 8080:8080 -v ./config/config.yml:/app/config.yml zoomrs
#
# Build stage
FROM golang:1.22-bullseye AS base

RUN adduser \
  --disabled-password \
  --gecos "" \
  --home "/nonexistent" \
  --shell "/sbin/nologin" \
  --no-create-home \
  --uid 65532 \
  api-user

ADD . /build
WORKDIR /build

# Build the application with the vendor dependencies and the cache
RUN --mount=type=cache,target="/root/.cache/go-build" make

# Production stage
# Not using scratch because of the CGO_ENABLED=1
FROM debian:stable-slim

WORKDIR /app

# Copy the certificates, passwd and group files from the base image
COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=base /etc/passwd /etc/passwd
COPY --from=base /etc/group /etc/group

# Copy the binary and the configuration file from the base image
COPY --from=base /build/dist/zoomrs .
# copy the configuration file or docker run with -v ./config/config.yml:/app/config/config.yml
# COPY --from=base /build/config/config.yml ./

# Change the ownership of the binary and the configuration file
RUN chown -R api-user:api-user /app
RUN chmod +x ./zoomrs

# Set the user to run the application
USER api-user:api-user

# Expose the port
EXPOSE 8080

CMD ["./zoomrs", "--config", "config.yml"]
