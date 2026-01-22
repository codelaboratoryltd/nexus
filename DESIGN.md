# Nexus Design Document

Central coordination service for distributed OLT-BNG edge networks.

## Overview

Nexus is the evolution of Neelix, expanding from a single-purpose IP allocator to a comprehensive coordination hub for edge infrastructure. It provides distributed resource management, OLT bootstrap, configuration distribution, and state synchronization across geographically distributed OLT-BNG nodes.

```
                    ┌─────────────────────────────────────┐
                    │         Nexus Cluster               │
                    │  (Central Coordination - K8s)       │
                    │                                     │
                    │  ┌─────────┐  ┌─────────┐          │
                    │  │ Nexus-1 │◄─┼─►Nexus-2│ (CLSet)  │
                    │  │ (write) │  │ (write) │          │
                    │  └────┬────┘  └────┬────┘          │
                    │       │            │               │
                    │  ┌────┴────────────┴────┐          │
                    │  │      Nexus-N         │          │
                    │  │      (read)          │          │
                    │  └──────────────────────┘          │
                    └─────────────┬───────────────────────┘
                                  │ Control Plane
           ┌──────────────────────┼──────────────────────┐
           │                      │                      │
           ▼                      ▼                      ▼
    ┌─────────────┐        ┌─────────────┐        ┌─────────────┐
    │  OLT-BNG 1  │        │  OLT-BNG 2  │        │  OLT-BNG N  │
    │ (Bare Metal)│        │ (Bare Metal)│        │ (Bare Metal)│
    │             │        │             │        │             │
    │ Subscribers │        │ Subscribers │        │ Subscribers │
    └─────────────┘        └─────────────┘        └─────────────┘
```

## Goals

1. **Distributed Resource Management**: Allocate IPs, VLANs, S-VLANs, ports across edge nodes
2. **Zero-Touch Provisioning**: OLT-BNGs self-register and receive configuration
3. **Offline-First**: Edge nodes continue operating during network partitions
4. **Eventual Consistency**: CLSet CRDT ensures convergence without coordination
5. **Horizontal Scaling**: Add nexus nodes for read capacity, OLT-BNGs for subscriber capacity

## Core Concepts

### Resource Types

Nexus manages multiple resource types using a generic allocator framework:

| Resource Type | Description | Sharding | Example |
|---------------|-------------|----------|---------|
| `ipv4` | IPv4 addresses | By CIDR subnet | `10.0.0.0/16` → 256 /24 shards |
| `ipv6` | IPv6 prefixes | By prefix | `2001:db8::/32` → /48 shards |
| `vlan` | C-VLAN IDs | By range | `100-4000` → ranges of 100 |
| `svlan` | S-VLAN IDs | By range | `1000-2000` |
| `port` | L4 ports | By range | `32768-65535` |

### Resource Pools

A pool defines a range of allocatable resources with metadata:

```yaml
pools:
  - id: "residential-ipv4"
    type: "ipv4"
    cidr: "100.64.0.0/10"      # CGNAT space
    sharding_factor: 100       # Virtual nodes in hashring
    exclusions:
      - "100.64.0.0/24"        # Reserved for infrastructure
    metadata:
      region: "east"
      service_type: "residential"

  - id: "business-vlans"
    type: "vlan"
    range: "1000-2000"
    sharding_factor: 10
    metadata:
      service_type: "business"
```

### Allocations

Allocations bind resources to subscribers:

```yaml
allocation:
  pool_id: "residential-ipv4"
  subscriber_id: "sub-12345"
  resource: "100.64.1.50"
  olt_id: "olt-east-01"
  timestamp: "2024-12-19T10:30:00Z"
  metadata:
    circuit_id: "0/1/5/32"
    mac: "aa:bb:cc:dd:ee:ff"
```

### OLT-BNG Nodes

OLT-BNG nodes register with nexus and receive resource shard assignments:

```yaml
olt:
  id: "olt-east-01"
  name: "East Data Center OLT"
  region: "east"
  address: "10.1.1.10:9000"
  capacity: 2000                # Max subscribers
  assigned_shards:
    - pool: "residential-ipv4"
      shards: [0, 1, 2, 3]      # Hashring assignments
    - pool: "business-vlans"
      shards: [0]
  state: "active"               # bootstrapping, active, draining, offline
  last_heartbeat: "2024-12-19T10:30:00Z"
```

## Architecture

### Technology Stack

| Component | Technology | Purpose |
|-----------|------------|---------|
| Distributed State | CLSet CRDT | Conflict-free state replication |
| Networking | libp2p | P2P communication between nodes |
| Pub/Sub | GossipSub | State change propagation |
| Local Storage | Badger | Persistent local state |
| Sharding | Virtual Hashring | Consistent resource distribution |
| API | HTTP + gRPC | External interfaces |
| Metrics | Prometheus | Observability |

### Node Roles

| Role | Description | Capabilities |
|------|-------------|--------------|
| `core` | Full participant | Read, write, hashring member |
| `write` | Write-capable | Read, write, hashring member |
| `read` | Read replica | Read-only, not in hashring |

### Package Structure

```
nexus/
├── cmd/
│   └── nexus/              # Main server
│
├── internal/
│   ├── api/                # HTTP handlers (REST API)
│   │   ├── api.go          # Router setup
│   │   ├── pools.go        # Pool endpoints
│   │   ├── allocations.go  # Allocation endpoints
│   │   └── nodes.go        # Node endpoints
│   │
│   ├── audit/              # Security audit logging
│   │   ├── logger.go       # Audit logger
│   │   ├── middleware.go   # HTTP audit middleware
│   │   └── types.go        # Event types
│   │
│   ├── validation/         # Input validation
│   │   └── validation.go   # Validation functions
│   │
│   ├── state/              # State management
│   │   └── manager.go      # Main state coordinator
│   │
│   ├── resource/           # Generic resource framework
│   │   ├── interfaces.go   # Resource, Allocator interfaces
│   │   └── types.go        # Resource types
│   │
│   ├── hashring/           # Consistent hashing
│   │   ├── hashring.go     # Basic hashring
│   │   └── virtual.go      # Virtual node support
│   │
│   ├── store/              # Persistence layer
│   │   ├── models.go       # Data models
│   │   ├── poolstore.go    # Pool CRUD
│   │   └── allocstore.go   # Allocation CRUD
│   │
│   ├── keys/               # Key generation
│   │   └── keys.go         # Key utilities
│   │
│   ├── ztp/                # Zero Touch Provisioning
│   │   └── handler.go      # ZTP handlers
│   │
│   └── util/               # Utilities
│       ├── marshal.go      # Marshalling helpers
│       └── ipconversion.go # IP conversion utilities
│
├── docs/
│   ├── COMPETITIVE_ANALYSIS.md  # VOLTHA/SEBA comparison
│   └── (see DESIGN.md in root)
│
├── Dockerfile
├── go.mod
└── README.md
```

**Planned but not yet implemented:**
- `cmd/cli/` - Management CLI
- `cmd/migrate/` - Data migration tools
- `api/proto/` - gRPC definitions (currently REST-only)
- `api/openapi.yaml` - OpenAPI spec

## API Design

### REST API (v1)

#### Health & Readiness (Implemented)

```
GET /health                             Health check
GET /ready                              Readiness check
```

#### Pools (Implemented)

```
GET    /api/v1/pools                    List all pools
POST   /api/v1/pools                    Create pool
GET    /api/v1/pools/{id}               Get pool details
DELETE /api/v1/pools/{id}               Delete pool
```

#### Allocations (Implemented)

```
GET    /api/v1/allocations              List allocations (requires pool_id query param)
POST   /api/v1/allocations              Create allocation
  Body: { pool_id, subscriber_id, ip (optional) }
  Response: { pool_id, subscriber_id, ip, timestamp }

GET    /api/v1/allocations/{subscriber_id}    Get subscriber allocation
DELETE /api/v1/allocations/{subscriber_id}    Delete allocation
```

#### Nodes (Implemented - Basic)

```
GET    /api/v1/nodes                    List OLT-BNG nodes
```

#### Bootstrap (Planned)

> **Note**: Bootstrap endpoints are planned but not yet implemented.

```
POST /api/v1/bootstrap
  Register new OLT-BNG node
  Body: { id, name, region, address, capacity }
  Response: { olt, assigned_pools, config }

GET /api/v1/bootstrap/{olt_id}/config
  Get current configuration for OLT

POST /api/v1/bootstrap/{olt_id}/heartbeat
  Update OLT heartbeat
```

### gRPC API (Planned)

> **Note**: gRPC is planned but not yet implemented. Currently all APIs are REST-only.
> The design below shows the intended future gRPC interface.

For high-frequency operations (allocation lookups from OLT-BNG):

```protobuf
// PLANNED - NOT YET IMPLEMENTED
service Nexus {
  // Bootstrap
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc GetConfig(ConfigRequest) returns (ConfigResponse);

  // Allocation (called by OLT-BNG)
  rpc Allocate(AllocateRequest) returns (AllocateResponse);
  rpc Deallocate(DeallocateRequest) returns (DeallocateResponse);
  rpc GetAllocation(GetAllocationRequest) returns (Allocation);

  // Streaming updates
  rpc WatchConfig(WatchRequest) returns (stream ConfigUpdate);
  rpc WatchAllocations(WatchRequest) returns (stream AllocationUpdate);
}
```

## Workflows

### OLT Bootstrap Flow

```
┌──────────┐                     ┌──────────┐
│  OLT-BNG │                     │  Nexus   │
└────┬─────┘                     └────┬─────┘
     │                                │
     │  1. POST /bootstrap            │
     │  {id, region, capacity}        │
     │ ───────────────────────────────►
     │                                │
     │                                │ 2. Assign to hashring
     │                                │    Calculate shard ownership
     │                                │
     │  3. Response                   │
     │  {pools, shards, config}       │
     │ ◄───────────────────────────────
     │                                │
     │  4. Initialize local           │
     │     allocators for shards      │
     │                                │
     │  5. Subscribe to updates       │
     │  gRPC WatchConfig stream       │
     │ ───────────────────────────────►
     │                                │
     │  6. Periodic heartbeat         │
     │ ───────────────────────────────►
     │                                │
```

### Resource Allocation Flow

IP allocation happens at RADIUS time, not DHCP:

```
┌────────────┐     ┌──────────┐     ┌──────────┐
│ Subscriber │     │  OLT-BNG │     │  Nexus   │
└─────┬──────┘     └────┬─────┘     └────┬─────┘
      │                 │                │
      │ 1. RADIUS Auth  │                │
      │ ────────────────►                │
      │                 │                │
      │                 │ 2. Allocate IP │
      │                 │ (if not cached)│
      │                 │ ───────────────►
      │                 │                │
      │                 │ 3. IP from     │
      │                 │    local shard │
      │                 │ ◄───────────────
      │                 │                │
      │ 4. RADIUS Accept│                │
      │    (with IP)    │                │
      │ ◄────────────────                │
      │                 │                │
      │ 5. DHCP Request │                │
      │ ────────────────►                │
      │                 │                │
      │ 6. DHCP Reply   │ (eBPF fast path)
      │    (cached IP)  │                │
      │ ◄────────────────                │
```

### Node Failure / Handoff

When an OLT-BNG fails, its shards are redistributed:

```
1. Nexus detects missing heartbeat (timeout)
2. Node marked as "offline" in membership
3. Hashring recalculates shard ownership
4. Affected OLT-BNGs receive new shard assignments
5. Allocators reinitialized with new ranges
6. Subscribers on failed node re-authenticate to new OLT
```

## CLSet Integration

### Why CLSet over go-ds-crdt?

| Aspect | go-ds-crdt | CLSet |
|--------|------------|-------|
| Origin | IPFS project | Custom (Vitrifi) |
| Optimized for | Content addressing | Resource allocation |
| Garbage collection | Complex | Simpler |
| Performance | Good | Better for our use case |
| Maintenance | External | Internal control |

### CLSet Operations

```go
// Put a value (wins on concurrent writes)
err := clset.Put(ctx, key, value)

// Get a value
value, err := clset.Get(ctx, key)

// Delete (tombstone)
err := clset.Delete(ctx, key)

// List with prefix
results, err := clset.Query(ctx, query.Query{Prefix: "/pools/"})
```

## Configuration

### Nexus Server

```yaml
# nexus.yaml
server:
  http_port: 9000
  grpc_port: 9001
  metrics_port: 9002

node:
  id: "nexus-east-01"        # Auto-generated if not set
  role: "core"               # core, write, read
  region: "east"

p2p:
  listen_port: 33123
  bootstrap:
    - "/ip4/10.0.0.1/tcp/33123/p2p/QmPeer1..."
    - "/ip4/10.0.0.2/tcp/33123/p2p/QmPeer2..."

storage:
  path: "/var/lib/nexus/data"

clset:
  topic: "nexus-state"
  rebroadcast_interval: 5s
  num_workers: 50

graceful_shutdown:
  timeout: 2m
  grace_period: 10s
```

### OLT-BNG Client Config (received from nexus)

```yaml
# Generated by nexus on bootstrap
olt_id: "olt-east-01"
nexus_endpoints:
  - "nexus-1.example.com:9001"
  - "nexus-2.example.com:9001"

pools:
  - id: "residential-ipv4"
    type: "ipv4"
    local_shards: [0, 1, 2, 3]
    shard_ranges:
      0: "100.64.0.0/18"
      1: "100.64.64.0/18"
      2: "100.64.128.0/18"
      3: "100.64.192.0/18"

  - id: "vlans"
    type: "vlan"
    local_shards: [0]
    shard_ranges:
      0: "100-500"

heartbeat_interval: 30s
config_watch: true
```

## Metrics

### Prometheus Metrics

```
# Resource pools
nexus_pool_total{pool_id, type}
nexus_pool_allocated{pool_id, type}
nexus_pool_available{pool_id, type}
nexus_pool_utilization_ratio{pool_id, type}

# Allocations
nexus_allocations_total{pool_id, olt_id, result}
nexus_allocation_duration_seconds{pool_id, quantile}

# Nodes
nexus_nodes_total{role, state}
nexus_node_subscribers{olt_id}
nexus_node_last_heartbeat_seconds{olt_id}

# CLSet
nexus_clset_puts_total
nexus_clset_gets_total
nexus_clset_sync_duration_seconds
nexus_clset_peers_total

# API
nexus_api_requests_total{method, path, status}
nexus_api_request_duration_seconds{method, path, quantile}
```

## Migration from Neelix

### Phase 1: Core Framework
- [ ] Set up nexus repo with neelix patterns
- [ ] Integrate CLSet (from preview/clset-integration)
- [ ] Implement generic resource framework
- [ ] Port IPv4 allocator to new interfaces

### Phase 2: Expanded Resources
- [ ] VLAN allocator
- [ ] S-VLAN allocator
- [ ] IPv6 prefix allocator

### Phase 3: OLT Coordination
- [ ] Bootstrap API
- [ ] Configuration distribution
- [ ] Heartbeat / health monitoring
- [ ] Shard reassignment on failure

### Phase 4: Production Hardening
- [ ] Graceful shutdown with peer ack
- [ ] Comprehensive metrics
- [ ] Operational tooling (CLI)
- [ ] Documentation

## Open Questions

1. **Multi-region**: How do we handle cross-region allocation?
   - Option A: Separate nexus clusters per region
   - Option B: Single cluster with region-aware sharding

2. **Session roaming**: When subscriber moves between OLTs
   - Keep allocation at original OLT?
   - Transfer allocation to new OLT?
   - Dual-home during transition?

3. **Backup allocations**: Pre-allocate backup IPs for HA?
   - Trade-off: Pool utilization vs failover speed

4. **Config versioning**: How to handle config rollback?
   - Git-like versioning?
   - Point-in-time snapshots?

## References

- Neelix codebase: `gitlab.com/vitrifi/borg/src/cne/neelix`
- CLSet integration: `preview/clset-integration` branch
- Generic allocator PoC: `poc/generic-resource-allocator-v2` branch
- BNG architecture: `docs/ebpf-dhcp-architecture.md`
