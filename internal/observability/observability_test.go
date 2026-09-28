package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthAndMetrics(t *testing.T) {
	health := &Health{}
	metrics := NewMetrics()
	handler := health.Handler(metrics)
	for _, ready := range []bool{false, true, false} {
		health.SetReady(ready)
		for _, path := range []string{"/livez", "/readyz", "/health/live", "/health/ready"} {
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
			want := http.StatusOK
			if (path == "/readyz" || path == "/health/ready") && !ready {
				want = http.StatusServiceUnavailable
			}
			if out.Code != want {
				t.Fatalf("ready=%v %s: %d", ready, path, out.Code)
			}
		}
	}
	metrics.ObserveUC("503", time.Millisecond)
	metrics.RPCs.WithLabelValues("unknown", "unsupported").Inc()
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest("GET", "/metrics", nil))
	if out.Code != 200 || !strings.Contains(out.Body.String(), `uc_requests_total{status="503"} 1`) || !strings.Contains(out.Body.String(), `adapter_requests_total{rpc="unknown",status="unsupported"} 1`) {
		t.Fatalf("missing Prometheus metrics: %s", out.Body.String())
	}
}

func TestStructuredLogging(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, slog.LevelInfo)
	logger.Debug("hidden")
	logger.Info("started", "component", "server")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != "started" || record["level"] != "INFO" || record["component"] != "server" {
		t.Fatalf("bad log %v", record)
	}
}

func TestVersion(t *testing.T) {
	out := httptest.NewRecorder()
	(&Health{}).Handler(NewMetrics()).ServeHTTP(out, httptest.NewRequest("GET", "/version", nil))
	var info map[string]string
	if out.Code != 200 || out.Header().Get("Content-Type") != "application/json" {
		t.Fatal("invalid version HTTP response")
	}
	if err := json.Unmarshal(out.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["service"] != "uc-trino-metastore-adapter" || info["version"] != Version || info["revision"] != Revision || info["git_sha"] != Revision || info["build_timestamp"] != BuildTimestamp || info["go_version"] == "" {
		t.Fatalf("unexpected version: %v", info)
	}
}
