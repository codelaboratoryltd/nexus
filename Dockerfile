FROM golang:1.25-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /app

# Download dependencies first (cacheable layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with version info
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.BuildVersion=${VERSION} -X main.BuildCommit=${COMMIT}" \
    -o nexus ./cmd/nexus

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /app/nexus /usr/local/bin/nexus

EXPOSE 9000 9001 9002 33123

ENTRYPOINT ["/usr/local/bin/nexus"]
CMD ["serve"]
