# Stage 1: Build the Go binary
FROM golang:latest AS builder

# Set the working directory
WORKDIR /app

# Copy the Go source code
COPY . .

# Build the binary for different platforms
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /app/markocaloriesbot .

# Stage 2: Create the final image
FROM alpine:latest

# Set the working directory inside the container.
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/markocaloriesbot /app/markocaloriesbot

# Give execution permissions to the binary if needed.
RUN chmod +x /app/markocaloriesbot

# Define the entrypoint to run the binary.
ENTRYPOINT ["/app/markocaloriesbot"]
