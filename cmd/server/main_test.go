package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability"
)

func TestRunLifecycle(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UC_BASE_URL", "https://uc.invalid/api/2.1/unity-catalog")
	t.Setenv("UC_CATALOG", "unity")
	t.Setenv("UC_TOKEN_FILE", token)
	t.Setenv("THRIFT_ADDRESS", "127.0.0.1:0")
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:0")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	events := make(chan map[string]any, 10)
	go func() {
		decoder := json.NewDecoder(reader)
		for {
			var event map[string]any
			if decoder.Decode(&event) != nil {
				return
			}
			events <- event
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- run(ctx, cfg, observability.NewLogger(writer, 0)) }()
	var started map[string]any
	select {
	case started = <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("startup timed out")
	}
	address, ok := started["http_address"].(string)
	if !ok {
		t.Fatalf("unexpected startup log %v", started)
	}
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/health/live", "/health/ready", "/metrics", "/version"} {
		resp, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
	if resp, err := client.Get("http://" + address + "/readyz"); err == nil {
		resp.Body.Close()
		t.Fatal("HTTP listener survived shutdown")
	}
}

func TestStartupRejectsUnreadableToken(t *testing.T) {
	t.Setenv("UC_BASE_URL", "https://uc.invalid/api/2.1/unity-catalog")
	t.Setenv("UC_CATALOG", "unity")
	t.Setenv("UC_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), cfg, observability.NewLogger(io.Discard, 0)); err == nil {
		t.Fatal("service started without credentials")
	}
}
