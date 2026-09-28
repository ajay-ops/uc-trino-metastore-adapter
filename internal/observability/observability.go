package observability

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Build metadata is supplied by build-time linker flags.
var Version = "dev"
var Revision = "unknown"
var BuildTimestamp = "unknown"

// NewLogger creates the JSON logger shared by the service.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Metrics owns a private registry; no process-global registrations are needed.
type Metrics struct {
	registry            *prometheus.Registry
	RPCs                *prometheus.CounterVec
	RPCLatency          *prometheus.HistogramVec
	UCRequests          *prometheus.CounterVec
	UCLatency           *prometheus.HistogramVec
	UCRetries           prometheus.Counter
	Connections         prometheus.Gauge
	RejectedConnections prometheus.Counter
}

// NewMetrics registers bounded-label protocol and upstream metrics.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry:            prometheus.NewRegistry(),
		RPCs:                prometheus.NewCounterVec(prometheus.CounterOpts{Name: "adapter_requests_total", Help: "HMS requests by known method and outcome."}, []string{"rpc", "status"}),
		RPCLatency:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "adapter_request_duration_seconds", Help: "HMS request latency."}, []string{"rpc"}),
		UCRequests:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "uc_requests_total", Help: "UC HTTP attempts by outcome."}, []string{"status"}),
		UCLatency:           prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "uc_request_duration_seconds", Help: "UC HTTP attempt latency."}, []string{"status"}),
		UCRetries:           prometheus.NewCounter(prometheus.CounterOpts{Name: "uc_retries_total", Help: "UC retry attempts."}),
		Connections:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "adapter_connections", Help: "Active Thrift connections."}),
		RejectedConnections: prometheus.NewCounter(prometheus.CounterOpts{Name: "adapter_connections_rejected_total", Help: "Connections rejected by admission limit."}),
	}
	m.registry.MustRegister(m.RPCs, m.RPCLatency, m.UCRequests, m.UCLatency, m.UCRetries, m.Connections, m.RejectedConnections, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// ObserveUC records an HTTP attempt without URL, table, token or user labels.
func (m *Metrics) ObserveUC(status string, elapsed time.Duration) {
	m.UCRequests.WithLabelValues(status).Inc()
	m.UCLatency.WithLabelValues(status).Observe(elapsed.Seconds())
}

// Health is ready only after listeners and credentials are initialized.
// Readiness does not assert upstream availability or end-to-end query success.
type Health struct{ ready atomic.Bool }

func (h *Health) SetReady(ready bool) { h.ready.Store(ready) }

// Handler exposes local health and metrics; it makes no UC request.
func (h *Health) Handler(m *Metrics) http.Handler {
	mux := http.NewServeMux()
	live := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("alive\n"))
	}
	ready := func(w http.ResponseWriter, r *http.Request) {
		if !h.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready (infrastructure only)\n"))
	}
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/ready", ready)
	// Retain the earlier health routes for existing callers.
	mux.HandleFunc("GET /livez", live)
	mux.HandleFunc("GET /readyz", ready)
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"service": "uc-trino-metastore-adapter", "version": Version, "revision": Revision, "git_sha": Revision, "build_timestamp": BuildTimestamp, "go_version": runtime.Version()})
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}
