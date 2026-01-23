FROM golang:1.25-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN --mount=type=ssh go mod download

# Copy source
COPY . .

# Build with version info
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.BuildVersion=${VERSION} -X main.BuildCommit=${COMMIT}" \
    -o nexus ./cmd/nexus

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/nexus /usr/local/bin/nexus

EXPOSE 9000 9001 9002 33123

ENTRYPOINT ["/usr/local/bin/nexus"]
CMD ["serve"]
