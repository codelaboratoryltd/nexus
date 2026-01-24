# Nexus

Central coordination service for BNG edge networks.

## Overview

Nexus is the evolution of Neelix, providing distributed resource management and coordination for OLT-BNG edge deployments. It uses CLSet CRDT for distributed state and libp2p for P2P communication.

## Features

- **Generic Resource Allocation**: IPs, VLANs, S-VLANs, ports
- **Virtual Hashring**: Consistent resource distribution across nodes (Rendezvous hashing)
- **TTL-Based Allocations**: Time-limited allocations with auto-expiration and renewal
- **OLT Bootstrap**: Zero-touch provisioning for edge nodes (Bootstrap API)
- **Failure Detection**: Automatic node failure detection with configurable thresholds
- **Shard Reassignment**: Automatic resource redistribution on node failure
- **Backup IP Pre-allocation**: HA failover with pre-allocated backup IPs
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

### Core Resources

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/ready` | GET | Readiness check |
| `/api/v1/pools` | GET | List resource pools |
| `/api/v1/pools` | POST | Create resource pool |
| `/api/v1/pools/{id}` | GET | Get pool details |
| `/api/v1/pools/{id}` | DELETE | Delete pool |
| `/api/v1/allocations` | GET | List allocations (requires pool_id) |
| `/api/v1/allocations` | POST | Create allocation (supports TTL) |
| `/api/v1/allocations/{subscriber_id}` | GET | Get subscriber allocation |
| `/api/v1/allocations/{subscriber_id}` | DELETE | Delete allocation |
| `/api/v1/allocations/{subscriber_id}/renew` | POST | Renew TTL allocation |
| `/api/v1/allocations/expiring` | GET | List expiring allocations |
| `/api/v1/nodes` | GET | List cluster nodes |

### Bootstrap API (ZTP)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/bootstrap/register` | POST | Register new device |
| `/api/v1/bootstrap/devices` | GET | List registered devices |
| `/api/v1/bootstrap/devices/{id}` | GET | Get device details |
| `/api/v1/bootstrap/devices/{id}/config` | GET | Get device configuration |

### TTL Allocation Example

```bash
# Create allocation with 2-hour TTL
curl -X POST http://localhost:9000/api/v1/allocations \
  -H "Content-Type: application/json" \
  -d '{"pool_id":"wifi","subscriber_id":"mac-aabbccdd","ttl":7200,"alloc_type":"sticky"}'

# Renew allocation
curl -X POST http://localhost:9000/api/v1/allocations/mac-aabbccdd/renew \
  -d '{"ttl":7200}'

# List allocations expiring within 1 hour
curl "http://localhost:9000/api/v1/allocations/expiring?within=3600"
```

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
│   ├── api/             # HTTP handlers (REST API)
│   ├── audit/           # Security audit logging
│   ├── validation/      # Input validation
│   ├── hashring/        # Consistent hashing
│   ├── resource/        # Generic resource framework
│   ├── state/           # State management
│   ├── store/           # Persistence layer
│   ├── keys/            # Key generation utilities
│   ├── ztp/             # Zero Touch Provisioning
│   └── util/            # Utility functions
├── docs/
│   └── COMPETITIVE_ANALYSIS.md
├── DESIGN.md            # Architecture documentation
├── Dockerfile
└── go.mod
```

## License

BSL 1.1 (Business Source License) - see [LICENSE](LICENSE)

Converts to Apache 2.0 on January 1, 2030.
