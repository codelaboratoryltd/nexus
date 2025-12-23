# Nexus

Central coordination service for BNG edge networks.

## Overview

Nexus is the evolution of Neelix, providing distributed resource management and coordination for OLT-BNG edge deployments. It uses CLSet CRDT for distributed state and libp2p for P2P communication.

## Features

- **Generic Resource Allocation**: IPs, VLANs, S-VLANs, ports
- **Virtual Hashring**: Consistent resource distribution across nodes
- **OLT Bootstrap**: Zero-touch provisioning for edge nodes
- **Distributed State**: CLSet CRDT for eventual consistency
- **Offline-First**: Edge nodes continue during partitions

## Quick Start

```bash
# Build
go build -o nexus ./cmd/nexus

# Run
./nexus serve

# With options
./nexus serve \
  --node-id=nexus-01 \
  --role=core \
  --http-port=9000 \
  --metrics-port=9002
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/ready` | GET | Readiness check |
| `/api/v1/pools` | GET | List resource pools |
| `/api/v1/pools` | POST | Create resource pool |
| `/api/v1/nodes` | GET | List cluster nodes |

## Ports

| Port | Protocol | Description |
|------|----------|-------------|
| 9000 | HTTP | REST API |
| 9001 | gRPC | gRPC API (future) |
| 9002 | HTTP | Prometheus metrics |
| 33123 | TCP | P2P communication |

## Docker

```bash
# Build
docker build -t ghcr.io/codelaboratoryltd/nexus:latest .

# Run
docker run -p 9000:9000 -p 9002:9002 \
  ghcr.io/codelaboratoryltd/nexus:latest
```

## Architecture

See [DESIGN.md](DESIGN.md) for detailed architecture documentation.

See [docs/COMPETITIVE_ANALYSIS.md](docs/COMPETITIVE_ANALYSIS.md) for comparison with VOLTHA/SEBA.

## Development

```bash
# Run tests
go test ./...

# Build
go build ./cmd/nexus

# Format
go fmt ./...
```

## Project Structure

```
nexus/
├── cmd/nexus/           # Main entry point
├── internal/
│   ├── api/             # HTTP/gRPC handlers
│   ├── hashring/        # Consistent hashing
│   ├── resource/        # Generic resource framework
│   ├── state/           # State management
│   └── store/           # Persistence
├── docs/
│   ├── DESIGN.md
│   └── COMPETITIVE_ANALYSIS.md
├── Dockerfile
└── go.mod
```

## License

Proprietary - Code Laboratory Ltd
