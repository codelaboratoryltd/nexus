FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download || true

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o nexus ./cmd/nexus

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/nexus /usr/local/bin/nexus

EXPOSE 9000 9001 9002

ENTRYPOINT ["/usr/local/bin/nexus"]
CMD ["serve"]
