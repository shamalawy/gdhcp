// Package metrics registers and exposes gdhcp's Prometheus metrics
// (docs/CONTRACTS.md Part 8). All metric families live on a single
// *Registry constructed by New(); callers record observations through its
// typed helper methods rather than reaching into raw prometheus vectors, so
// label names/cardinality stay centralized and consistent.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Naming convention (docs/CONTRACTS.md Part 8): gdhcp_{subsystem}_{metric_name}_{unit}.
const namespace = "gdhcp"

// Registry holds every gdhcp metric family on its own prometheus.Registry
// (not the global default registry, so multiple *Registry instances — e.g.
// in tests — never collide).
type Registry struct {
	reg *prometheus.Registry

	// DHCP protocol transaction counters, one per message type, labeled by
	// scope_id.
	dhcpDiscover *prometheus.CounterVec
	dhcpOffer    *prometheus.CounterVec
	dhcpRequest  *prometheus.CounterVec
	dhcpAck      *prometheus.CounterVec
	dhcpNak      *prometheus.CounterVec
	dhcpDecline  *prometheus.CounterVec
	dhcpRelease  *prometheus.CounterVec
	dhcpNoLeases *prometheus.CounterVec

	// Lease allocation (amendment A6 retry loop).
	allocationRetries *prometheus.HistogramVec
	leaseConflicts    *prometheus.CounterVec

	// HA cluster: heartbeat, election, VIP, DB partition detection.
	heartbeatWriteDuration *prometheus.HistogramVec
	electionQueryDuration  *prometheus.HistogramVec
	vipAssignmentFailures  *prometheus.CounterVec
	vipAssigned            *prometheus.GaugeVec
	dbPartitionDetected    *prometheus.CounterVec

	// HTTP API.
	apiRequestsTotal   *prometheus.CounterVec
	apiRequestDuration *prometheus.HistogramVec

	// Database connection pool (pgxpool.Stat snapshot; single pool per process).
	dbPoolOpenConns  prometheus.Gauge
	dbPoolIdleConns  prometheus.Gauge
	dbPoolInUseConns prometheus.Gauge

	// Process/build identity.
	buildInfo *prometheus.GaugeVec
}

// New creates a Registry, registers every metric family plus the standard Go
// runtime/process collectors, and returns it ready for use.
func New() *Registry {
	reg := prometheus.NewRegistry()

	r := &Registry{
		reg: reg,

		dhcpDiscover: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "discover_total",
			Help: "Total DHCPDISCOVER messages received, by scope.",
		}, []string{"scope_id"}),
		dhcpOffer: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "offer_total",
			Help: "Total DHCPOFFER messages sent, by scope.",
		}, []string{"scope_id"}),
		dhcpRequest: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "request_total",
			Help: "Total DHCPREQUEST messages received, by scope.",
		}, []string{"scope_id"}),
		dhcpAck: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "ack_total",
			Help: "Total DHCPACK messages sent, by scope.",
		}, []string{"scope_id"}),
		dhcpNak: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "nak_total",
			Help: "Total DHCPNAK messages sent, by scope.",
		}, []string{"scope_id"}),
		dhcpDecline: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "decline_total",
			Help: "Total DHCPDECLINE messages received, by scope.",
		}, []string{"scope_id"}),
		dhcpRelease: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "release_total",
			Help: "Total DHCPRELEASE messages received, by scope.",
		}, []string{"scope_id"}),
		dhcpNoLeases: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "no_leases_total",
			Help: "Total requests that could not be satisfied because the pool was exhausted, by scope.",
		}, []string{"scope_id"}),

		allocationRetries: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "dhcp", Name: "allocation_retries",
			Help:    "Number of candidate-IP retry attempts taken to allocate a lease (amendment A6), by scope.",
			Buckets: []float64{0, 1, 2, 3},
		}, []string{"scope_id"}),
		leaseConflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "lease", Name: "allocation_conflicts_total",
			Help: "Total ON CONFLICT DO NOTHING races lost during lease allocation, by pool.",
		}, []string{"pool_id"}),

		heartbeatWriteDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "heartbeat_write_duration_ms",
			Help:    "Duration of a primary's heartbeat write, in milliseconds, by server.",
			Buckets: []float64{5, 10, 25, 50, 100, 250, 500, 1000},
		}, []string{"server_id"}),
		electionQueryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Name: "election_query_duration_ms",
			Help:    "Duration of an election query, in milliseconds, by cluster.",
			Buckets: []float64{10, 25, 50, 100, 250, 500, 1000, 2000},
		}, []string{"cluster_id"}),
		vipAssignmentFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "vip_assignment_failures_total",
			Help: "Total failed VIP assignment attempts (netlink errors), by cluster.",
		}, []string{"cluster_id"}),
		vipAssigned: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "vip_assigned",
			Help: "Whether this node currently holds the cluster VIP (1) or not (0), by cluster.",
		}, []string{"cluster_id"}),
		dbPartitionDetected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "db_partition_detected_total",
			Help: "Total times this node detected it was partitioned from the database, by cluster.",
		}, []string{"cluster_id"}),

		apiRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Subsystem: "api", Name: "requests_total",
			Help: "Total HTTP API requests, by method, route, and status code.",
		}, []string{"method", "route", "status"}),
		apiRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace, Subsystem: "api", Name: "request_duration_ms",
			Help:    "HTTP API request duration, in milliseconds, by method and route.",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500},
		}, []string{"method", "route"}),

		dbPoolOpenConns: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "db", Name: "pool_open_connections",
			Help: "Current number of established connections in the pgxpool (idle + in-use).",
		}),
		dbPoolIdleConns: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "db", Name: "pool_idle_connections",
			Help: "Current number of idle connections in the pgxpool.",
		}),
		dbPoolInUseConns: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace, Subsystem: "db", Name: "pool_in_use_connections",
			Help: "Current number of connections in the pgxpool actively serving a query.",
		}),

		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "build_info",
			Help: "Always 1; labels carry the running build's version/commit/build_date.",
		}, []string{"version", "commit", "build_date"}),
	}

	reg.MustRegister(
		r.dhcpDiscover, r.dhcpOffer, r.dhcpRequest, r.dhcpAck, r.dhcpNak,
		r.dhcpDecline, r.dhcpRelease, r.dhcpNoLeases,
		r.allocationRetries, r.leaseConflicts,
		r.heartbeatWriteDuration, r.electionQueryDuration,
		r.vipAssignmentFailures, r.vipAssigned, r.dbPartitionDetected,
		r.apiRequestsTotal, r.apiRequestDuration,
		r.dbPoolOpenConns, r.dbPoolIdleConns, r.dbPoolInUseConns,
		r.buildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return r
}

// SetBuildInfo publishes the running build's version identity as a gauge.
// Call once at startup with version.Get()'s fields.
func (r *Registry) SetBuildInfo(ver, commit, buildDate string) {
	r.buildInfo.WithLabelValues(ver, commit, buildDate).Set(1)
}

// Handler returns the http.Handler to mount at metrics.listen_addr + path
// (config.MetricsConfig).
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{Registry: r.reg})
}
