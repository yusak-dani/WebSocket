# Stage 1: Build the Go executable
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum first to cache dependencies
COPY server/go.mod server/go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the application source code
COPY server/ ./

# Build the application statically
# -o main: Output binary name
# CGO_ENABLED=0: Disable CGO for a fully static binary
# GOOS=linux: Target Linux OS
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Stage 2: Create a minimal runtime image
FROM alpine:latest

WORKDIR /root/

# Copy the pre-built binary from the builder stage
COPY --from=builder /app/main .

# Expose the port (Render will override this with its own PORT env var, but good for local/docs)
EXPOSE 8080

# Run the executable
CMD ["./main"]
