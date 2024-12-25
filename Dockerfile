# Build stage
FROM golang:latest AS builder

WORKDIR /app
COPY . .

# WORKDIR /app/pocker

# # Download dependencies
# RUN go mod download

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o pocker ./examples/fly/main.go

# Final stage
FROM alpine:latest

WORKDIR /

# Copy the binary from builder
COPY --from=builder /app/pocker .

# Create data directory for persistence
RUN mkdir /data

# Expose the port specified in fly.toml
EXPOSE 8080

# Run the binary
CMD ["/pocker"] 