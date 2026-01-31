FROM golang:1.25-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /app

# Copy everything including vendor directory
COPY . .

# Build with version info using vendored dependencies
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -mod=vendor \
    -ldflags "-X main.BuildVersion=${VERSION} -X main.BuildCommit=${COMMIT}" \
    -o nexus ./cmd/nexus

# Runtime image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /app/nexus /usr/local/bin/nexus

EXPOSE 9000 9001 9002 33123

ENTRYPOINT ["/usr/local/bin/nexus"]
CMD ["serve"]
# Dockerfile modified: vendored dependencies, curl for init container
