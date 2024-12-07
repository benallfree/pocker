# Build stage
FROM golang:latest AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o pocker ./examples/pocker/main.go

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