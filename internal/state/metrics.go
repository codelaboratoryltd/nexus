package state

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// crdtSyncLagGauge tracks the maximum CRDT sync lag across all peers in seconds.
	crdtSyncLagGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "nexus",
		Subsystem: "crdt",
		Name:      "sync_lag_seconds",
		Help:      "Maximum CRDT sync lag across all peers in seconds.",
	})

	// crdtPeersConnectedGauge tracks the number of peers with CRDT sync data.
	crdtPeersConnectedGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "nexus",
		Subsystem: "crdt",
		Name:      "peers_connected",
		Help:      "Number of peers with CRDT sync data.",
	})
)
