# Build stage
FROM golang:alpine AS builder

RUN apk add --no-cache wget

ENV GEESEFS_VERSION=v0.42.0-tigris1
ENV GEESEFS_BIN=geesefs-linux-amd64
RUN wget -O /usr/local/bin/geesefs https://github.com/tigrisdata/geesefs/releases/download/${GEESEFS_VERSION}/${GEESEFS_BIN}
RUN chmod +x /usr/local/bin/geesefs


WORKDIR /app
COPY . .

# WORKDIR /app/pocker

# # Download dependencies
# RUN go mod download

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o pocker ./examples/fly/main.go

# Final stage
FROM alpine:latest

# Install supervisord and other dependencies
RUN apk add --no-cache supervisor fuse3

# Create data directory for persistence
RUN mkdir -p /data
RUN mkdir -p /data/geesefs-cache
RUN mkdir -p /mnt/data

# Install geesefs
COPY --from=builder /usr/local/bin/geesefs /usr/local/bin/geesefs

# Set up supervisord configuration
COPY docker/supervisord.conf /etc/supervisord.conf

# Copy the binary from builder
COPY --from=builder /app/pocker .

# Expose the port specified in fly.toml
EXPOSE 8080

# Run supervisord
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]