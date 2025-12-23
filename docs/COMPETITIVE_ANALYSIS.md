# Competitive Analysis: BNG + Nexus vs VOLTHA/SEBA

## Executive Summary

Our OLT-BNG approach fundamentally differs from VOLTHA/SEBA by pushing BNG functionality to the edge (on OLT hardware) rather than disaggregating it to a central cloud. This eliminates subscriber traffic hairpinning, reduces latency, lowers backhaul costs, and improves offline resilience.

## VOLTHA/SEBA Overview

VOLTHA (Virtual OLT Hardware Abstraction) and SEBA (SDN-Enabled Broadband Access) are open-source projects from the Open Networking Foundation (now Linux Foundation Broadband) for disaggregated broadband access.

### Architecture

```
VOLTHA/SEBA Architecture:

┌─────────────────────────────────────────────────────────────┐
│                    Central Data Center                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │  ONOS    │  │   ETCD   │  │  Kafka   │  │   BNG    │    │
│  │(SDN Ctrl)│  │ (State)  │  │(Messaging│  │ (Cloud)  │    │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘    │
│       └─────────────┴─────────────┴─────────────┘          │
│                           │                                 │
│  ┌────────────────────────┴────────────────────────┐       │
│  │              VOLTHA Stack (per OLT)              │       │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐       │       │
│  │  │  Core    │  │ OLT Adpt │  │ ONU Adpt │       │       │
│  │  └──────────┘  └──────────┘  └──────────┘       │       │
│  └──────────────────────┬───────────────────────────┘       │
└─────────────────────────┼───────────────────────────────────┘
                          │ Backhaul (all subscriber traffic)
                          ▼
              ┌───────────────────────┐
              │   White-box OLT       │
              │   (dumb, controlled)  │
              └───────────────────────┘
                          │
                   Subscribers
```

### Key Characteristics

| Aspect | Details |
|--------|---------|
| OLT Role | Dumb white-box, remotely controlled |
| BNG Location | Centralized (cloud/data center) |
| Control Plane | ONOS SDN controller |
| State Storage | ETCD (3-node cluster) |
| Messaging | Kafka (3-node cluster) |
| Scale | 1,024 ONUs per VOLTHA stack |
| Deployment | Kubernetes required |

### Production Deployments

- Deutsche Telekom (Access 4.0)
- Türk Telekom (world's largest SEBA deployment)
- Telecom Italia (since VOLTHA 2.9)

## Our Approach: OLT-BNG + Nexus

```
Our Architecture:

┌─────────────────────────────────────────────────────────────┐
│              Nexus Cluster (Control Plane Only)             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │ Nexus-1  │◄─┼─►Nexus-2 │◄─┼─►Nexus-N │  (CLSet CRDT)   │
│  └──────────┘  └──────────┘  └──────────┘                  │
│       P2P state sync (libp2p) - NO subscriber traffic       │
└─────────────────────────┬───────────────────────────────────┘
                          │ Config/Coordination only
           ┌──────────────┼──────────────┐
           ▼              ▼              ▼
    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
    │  OLT-BNG 1  │ │  OLT-BNG 2  │ │  OLT-BNG N  │
    │  (eBPF/XDP) │ │  (eBPF/XDP) │ │  (eBPF/XDP) │
    │             │ │             │ │             │
    │ BNG HERE ───┼─┼─► Internet  │ │             │
    └─────────────┘ └─────────────┘ └─────────────┘
           │              │              │
      Subscribers    Subscribers    Subscribers
      (traffic local) (traffic local) (traffic local)
```

### Key Characteristics

| Aspect | Details |
|--------|---------|
| OLT Role | Smart - runs full BNG (eBPF/XDP) |
| BNG Location | Edge (on OLT hardware) |
| Control Plane | Nexus (distributed, P2P) |
| State Storage | CLSet CRDT (distributed) |
| Messaging | libp2p GossipSub |
| Scale | 1-2K subscribers per OLT-BNG |
| Deployment | Bare metal (systemd) |

## Why Our Approach is Better

### 1. No Traffic Hairpinning

**VOLTHA/SEBA Problem:**
> "Traffic may backhaul over the congested and unpredictable public Internet. The same problem manifests itself with traffic having to be steered over huge distances, backhauled and hairpinned between multiple locations."
> — [Subspace: Hairpinning and Traffic Backhauling](https://subspace.com/resources/hairpinning-and-traffic-backhauling)

**Our Solution:**
Subscriber traffic terminates at the OLT-BNG and goes directly to the internet. No hairpin. No backhaul through central BNG.

```
VOLTHA:    Subscriber → OLT → Backhaul → Central BNG → Internet
                                    ↑
                            Latency + cost here

Ours:      Subscriber → OLT-BNG → Internet
                           ↑
                    Direct path
```

### 2. Lower Latency

**VOLTHA/SEBA Problem:**
> "Every processing step a packet goes through adds microseconds of latency. At millions of packets per second, this overhead quickly becomes a performance bottleneck."
> — [Sematext: eBPF and XDP](https://sematext.com/blog/ebpf-and-xdp-for-processing-packets-at-bare-metal-speed/)

**Our Solution:**
eBPF/XDP processes packets at the earliest point in the network stack, before kernel data structures are built.

| Processing Location | Typical Latency |
|---------------------|-----------------|
| Central BNG (VOLTHA) | 1-10+ ms (network RTT) |
| Userspace (DPDK/VPP) | 10-100 μs |
| eBPF/XDP (kernel) | 1-10 μs |
| eBPF/XDP fast path | <1 μs |

> "XDP can achieve throughput as high as 24 million packets per second (Mpps) per core."
> — [iximiuz Labs: XDP Fundamentals](https://labs.iximiuz.com/tutorials/ebpf-xdp-fundamentals-6342d24e)

### 3. Reduced Backhaul Costs

**VOLTHA/SEBA Problem:**
> "Centralized BNGs are easier to manage, but in large networks with high subscriber densities it is better to locate BNG systems closer to the subscribers to reduce backhaul costs and latency."
> — [Nokia: Disaggregating the BNG](https://www.nokia.com/blog/disaggregating-broadband-network-gateway/)

**Our Solution:**
Only control plane traffic (config, metrics, coordination) traverses the backhaul. Subscriber data traffic stays local.

| Traffic Type | VOLTHA | Ours |
|--------------|--------|------|
| Subscriber data | Via backhaul | Local |
| Control plane | Via backhaul | Via backhaul |
| Backhaul bandwidth | High (all traffic) | Low (control only) |

### 4. Offline Resilience

**VOLTHA/SEBA Problem:**
Depends on central ONOS controller, ETCD, and Kafka. If backhaul fails, OLTs lose coordination.

**Our Solution:**
- OLT-BNG continues operating during network partitions
- CLSet CRDT ensures eventual consistency when reconnected
- No single point of failure for subscriber service

```
Backhaul failure scenario:

VOLTHA:    OLT loses controller → service degradation
Ours:      OLT-BNG continues → service unaffected
           (syncs when backhaul restored)
```

### 5. Simpler Operations

**VOLTHA/SEBA Requirements:**
- Kubernetes cluster
- 3-node ETCD cluster
- 3-node Kafka cluster
- ONOS SDN controller
- Multiple VOLTHA stacks (one per OLT)
- Helm charts, operators, persistent volumes

> "VOLTHA v2.8 meets operator scale requirements with persistence enabled on ETCD... they moved to NVMe disks on AWS and implemented an ETCD connection pool."
> — [ONF: VOLTHA LTS Release](https://opennetworking.org/news-and-events/blog/voltha-seba-builds-on-long-term-support-release/)

**Our Requirements:**
- Nexus cluster (Kubernetes, but smaller)
- OLT-BNG (bare metal, systemd service)

| Component | VOLTHA | Ours |
|-----------|--------|------|
| Central infra | K8s + ETCD + Kafka + ONOS | K8s + Nexus |
| Per-OLT | VOLTHA stack (K8s pods) | systemd service |
| State sync | ETCD (centralized) | CLSet (P2P) |
| Messaging | Kafka | libp2p GossipSub |

### 6. Better Resource Efficiency

**VOLTHA/SEBA Problem:**
> "DPDK and similar solutions often require dedicating one or more CPU cores solely for packet processing, which limits scalability and efficiency."
> — [iximiuz Labs](https://labs.iximiuz.com/tutorials/ebpf-xdp-fundamentals-6342d24e)

**Our Solution:**
eBPF/XDP doesn't require dedicated CPU cores. The kernel schedules BPF programs efficiently alongside other workloads.

### 7. Edge-Native Architecture

**VOLTHA/SEBA Problem:**
> "The classic, centralized broadband network gateway approach may have worked well in the past, but its rigid architecture makes it difficult to adopt service-oriented applications at the network's edge."
> — [Nokia](https://www.nokia.com/blog/disaggregating-broadband-network-gateway/)

**Our Solution:**
BNG runs at the edge by design. Future edge services (caching, compute) integrate naturally at the OLT-BNG.

## Where VOLTHA/SEBA is Better

To be fair, VOLTHA/SEBA has advantages:

| Aspect | VOLTHA Advantage |
|--------|------------------|
| Maturity | Production-proven (DT, Türk Telekom) |
| Vendor support | Multiple vendors, established ecosystem |
| OLT abstraction | Works with any white-box OLT |
| Standards | ONF/BBF alignment |
| Documentation | Extensive docs and community |

Our approach requires:
- OLT hardware capable of running Linux + eBPF
- New operational model (edge-centric)
- Less mature ecosystem

## Comparison Summary

| Aspect | VOLTHA/SEBA | OLT-BNG + Nexus | Winner |
|--------|-------------|-----------------|--------|
| Traffic path | Hairpin via central BNG | Direct at edge | **Ours** |
| Latency | ms (network RTT) | μs (local) | **Ours** |
| Backhaul cost | High (all traffic) | Low (control only) | **Ours** |
| Offline resilience | Controller-dependent | Autonomous | **Ours** |
| Ops complexity | High (K8s + ETCD + Kafka) | Lower | **Ours** |
| Production maturity | Proven | New | VOLTHA |
| Vendor ecosystem | Established | Emerging | VOLTHA |
| Standards alignment | ONF/BBF | Custom | VOLTHA |

## Conclusion

VOLTHA/SEBA represents the "disaggregate everything to the cloud" philosophy. Our approach represents "push intelligence to the edge" philosophy.

For ISPs prioritizing:
- **Low latency** → Our approach
- **Backhaul cost reduction** → Our approach
- **Offline resilience** → Our approach
- **Operational simplicity** → Our approach
- **Proven production deployments** → VOLTHA
- **Multi-vendor OLT ecosystem** → VOLTHA

The industry is moving toward edge computing. Our architecture is better positioned for this future.

## References

### VOLTHA/SEBA
- [SEBA/VOLTHA - Open Networking Foundation](https://opennetworking.org/voltha/)
- [VOLTHA Architecture Overview](https://docs.voltha.org/master/overview/architecture_overview.html)
- [SEBA Reference Design v2.0](https://opennetworking.org/news-and-events/blog/draft-seba-reference-design-v2-0-adds-bng-disaggregation-scaling-and-new-services-for-broadband-access-networks/)
- [LF Broadband Announcement](https://lfbroadband.org/lf-broadband-drives-development-and-adoption-of-open-source-broadband-transforming-disaggregated-pon-architectures/)

### eBPF/XDP Performance
- [eBPF and XDP for Processing Packets at Bare-metal Speed](https://sematext.com/blog/ebpf-and-xdp-for-processing-packets-at-bare-metal-speed/)
- [XDP Fundamentals - iximiuz Labs](https://labs.iximiuz.com/tutorials/ebpf-xdp-fundamentals-6342d24e)
- [eBPF/XDP vs P4 vs DPDK](https://medium.com/@tom_84912/ebpf-xdp-vs-p4-vs-dpdk-the-ultimate-smackdown-4855d8284f5e)

### BNG Architecture
- [Nokia: Disaggregating the BNG](https://www.nokia.com/blog/disaggregating-broadband-network-gateway/)
- [Hairpinning and Traffic Backhauling](https://subspace.com/resources/hairpinning-and-traffic-backhauling)
