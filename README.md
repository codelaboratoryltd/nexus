# Nexus

Central coordination service for BNG edge network.

## Features

- **Bootstrap API**: OLT registration and configuration
- **Subscriber State**: Distributed subscriber management (CLSet CRDT)
- **IP Allocation**: Consistent hashring-based IP assignment
- **Configuration Distribution**: Push config to edge OLT-BNG nodes

## Quick Start

```bash
# Build
go build -o nexus ./cmd/nexus

# Run with in-memory store (development)
./nexus serve --store=memory

# Run with etcd (production)
./nexus serve --store=etcd --etcd-addr=localhost:2379
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/ready` | GET | Readiness check |
| `/api/v1/bootstrap` | POST | OLT registration |
| `/api/v1/subscribers` | GET/POST | Subscriber management |

## Ports

| Port | Protocol | Description |
|------|----------|-------------|
| 9000 | HTTP | REST API |
| 9001 | gRPC | gRPC API |
| 9002 | HTTP | Prometheus metrics |

## Docker

```bash
# Build
docker build -t ghcr.io/codelaboratoryltd/nexus:latest .

# Run
docker run -p 9000:9000 -p 9001:9001 -p 9002:9002 \
  ghcr.io/codelaboratoryltd/nexus:latest
```

## Architecture

Nexus is part of the BNG edge infrastructure:

```
┌─────────────────────────────────────────┐
│  Central Coordination (Kubernetes)       │
│  ├── Nexus ← you are here               │
│  ├── Prometheus/Grafana                  │
│  └── Image Registry                      │
└─────────────────────────────────────────┘
        │ Control Plane Only
        ▼
┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│ OLT-BNG 1   │ │ OLT-BNG 2   │ │ OLT-BNG N   │
│ (Bare Metal)│ │ (Bare Metal)│ │ (Bare Metal)│
└─────────────┘ └─────────────┘ └─────────────┘
```

## Development

For local development with k3d/Tilt, use the [bng-edge-infra](https://github.com/codelaboratoryltd/bng-edge-infra) repository.
