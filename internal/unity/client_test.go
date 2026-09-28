package unity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
)

func newClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{UCBaseURL: server.URL + "/api/2.1/unity-catalog", UCCatalog: "unity", UCTokenFile: tokenFile, AllowInsecureHTTP: true,
		ThriftAddress: ":0", HTTPAddress: ":0", ConnectTimeout: time.Second, RequestTimeout: time.Second, OperationTimeout: 3 * time.Second,
		RetryAttempts: 2, RetryInitialBackoff: time.Millisecond, RetryMaxBackoff: time.Millisecond,
		MaxResponseBytes: 4096, MaxListBytes: 40960, MaxPages: 10, MaxItems: 10, CacheMaxEntries: 10, ShutdownTimeout: time.Second, SocketTimeout: time.Second, MaxConnections: 4, MaxMessageBytes: 4096}
	client, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, server
}

func TestGetAuthenticationEncodingAndRotation(t *testing.T) {
	auth := make(chan string, 2)
	client, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth <- r.Header.Get("Authorization")
		if r.Method != "GET" || r.URL.Path != "/api/2.1/unity-catalog/schemas" || r.URL.Query().Get("page_token") != "a+b & c" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"value":7,"future_field":true}`))
	})
	for _, token := range []string{"test-token", "rotated-token"} {
		if err := os.WriteFile(client.cfg.UCTokenFile, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Value int `json:"value"`
		}
		if err := client.Get(context.Background(), "schemas", url.Values{"page_token": {"a+b & c"}}, &result); err != nil {
			t.Fatal(err)
		}
		if got := <-auth; result.Value != 7 || got != "Bearer "+token {
			t.Fatalf("bad response/auth: %v %v", result, got)
		}
	}
}

func TestHTTPFailuresAndRetries(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500, 502, 503, 504, 501} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			client, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error_code":"TEST_ERROR","message":"secret-test-token"}`))
			})
			var out any
			err := client.Get(context.Background(), "tables", nil, &out)
			var ue *Error
			if !errors.As(err, &ue) || ue.StatusCode != status || ue.Code != "TEST_ERROR" || ue.Message != "secret-test-token" {
				t.Fatalf("missing structured error: %#v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("error leaks message")
			}
			want := int32(1)
			if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
				want = 2
			}
			if calls.Load() != want {
				t.Fatalf("calls=%d want=%d", calls.Load(), want)
			}
		})
	}
	t.Run("recovery", func(t *testing.T) {
		var calls atomic.Int32
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`[]`))
		})
		var out []string
		if err := c.Get(context.Background(), "schemas", nil, &out); err != nil || calls.Load() != 2 {
			t.Fatalf("%v calls=%d", err, calls.Load())
		}
	})
	t.Run("retry-after-exceeds-budget", func(t *testing.T) {
		var calls atomic.Int32
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(429)
		})
		var out any
		err := c.Get(context.Background(), "schemas", nil, &out)
		if err == nil || calls.Load() != 1 {
			t.Fatalf("%v calls=%d", err, calls.Load())
		}
	})
}

func TestResponseValidationAndNoRedirects(t *testing.T) {
	for _, body := range []string{"broken", `{"a":1} {"b":2}`, strings.Repeat("x", 4097)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			var calls atomic.Int32
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(body)) })
			var out any
			if err := c.Get(context.Background(), "tables", nil, &out); err == nil || calls.Load() != 1 {
				t.Fatalf("expected nonretryable decode error: %v", err)
			}
		})
	}
	var destinationCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer target.Close()
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	var out any
	if err := c.Get(context.Background(), "tables", nil, &out); err == nil || destinationCalls.Load() != 0 {
		t.Fatal("redirect followed or succeeded")
	}
	for _, path := range []string{"https://attacker.invalid/", "//attacker.invalid/", "/tables", "../tables", "%2e%2e/tables", "tables?token=bad", "tables#bad"} {
		var e *Error
		if err := c.Get(context.Background(), path, nil, &out); !errors.As(err, &e) || e.Kind != "invalid_resource" {
			t.Errorf("path %q accepted: %v", path, err)
		}
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	t.Run("during-http", func(t *testing.T) {
		started := make(chan struct{})
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() { var out any; result <- c.Get(ctx, "tables", nil, &out) }()
		<-started
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation blocked")
		}
	})
	t.Run("during-backoff", func(t *testing.T) {
		reached := make(chan struct{}, 1)
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			reached <- struct{}{}
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(503)
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() { var out any; result <- c.Get(ctx, "tables", nil, &out) }()
		<-reached
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("backoff ignored context")
		}
	})
	t.Run("request-timeout", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		c.http.Timeout = 20 * time.Millisecond
		c.cfg.RetryAttempts = 1
		var out any
		err := c.Get(context.Background(), "tables", nil, &out)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected timeout: %v", err)
		}
	})
	t.Run("already-cancelled", func(t *testing.T) {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var out any
		if err := c.Get(ctx, "tables", nil, &out); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestTokenFailure(t *testing.T) {
	for _, token := range []string{"", "  ", "bad\ntoken", strings.Repeat("x", 16385)} {
		t.Run(fmt.Sprint(len(token)), func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("request sent with invalid token") })
			if err := os.WriteFile(c.cfg.UCTokenFile, []byte(token), 0600); err != nil {
				t.Fatal(err)
			}
			var out any
			if err := c.Get(context.Background(), "tables", nil, &out); err == nil {
				t.Fatal("accepted invalid token")
			}
			if _, err := New(c.cfg, nil); err == nil {
				t.Fatal("startup accepted invalid token")
			}
		})
	}
}

func TestPagination(t *testing.T) {
	tests := []struct {
		name               string
		pages              []string
		maxPages, maxItems int
		wantErr            string
		wantItems          int
	}{
		{"complete", []string{`{"items":[1],"next_page_token":"a+b"}`, `{"items":[],"next_page_token":"last"}`, `{"items":[2]}`}, 10, 10, "", 2},
		{"empty", []string{`{"items":[]}`}, 10, 10, "", 0},
		{"cycle", []string{`{"items":[1],"next_page_token":"x"}`, `{"items":[2],"next_page_token":"x"}`}, 10, 10, "pagination_cycle", 0},
		{"limit-pages", []string{`{"items":[1],"next_page_token":"x"}`}, 1, 10, "pagination_limit", 0},
		{"limit-items", []string{`{"items":[1,2]}`}, 10, 1, "pagination_limit", 0},
		{"missing-list", []string{`{"other":[]}`}, 10, 10, "invalid_response", 0},
		{"null-list", []string{`{"items":null}`}, 10, 10, "invalid_response", 0},
		{"wrong-token", []string{`{"items":[],"next_page_token":1}`}, 10, 10, "invalid_response", 0},
		{"partial-failure", []string{`{"items":[1],"next_page_token":"x"}`, `FAIL`}, 10, 10, "pagination_failed", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				i := int(calls.Add(1)) - 1
				if i >= len(tt.pages) {
					t.Error("unexpected extra page")
					w.WriteHeader(500)
					return
				}
				if tt.pages[i] == "FAIL" {
					w.WriteHeader(403)
					return
				}
				if i == 1 && tt.name == "complete" && r.URL.Query().Get("page_token") != "a+b" {
					t.Error("token was not preserved")
				}
				_, _ = w.Write([]byte(tt.pages[i]))
			})
			c.cfg.MaxPages = tt.maxPages
			c.cfg.MaxItems = tt.maxItems
			query := url.Values{"catalog_name": {"unity"}}
			items, err := CollectPages[int](context.Background(), c, "items", query, "items")
			if tt.wantErr == "" {
				if err != nil || len(items) != tt.wantItems {
					t.Fatalf("%v %v", items, err)
				}
			} else {
				var e *Error
				if !errors.As(err, &e) || e.Kind != tt.wantErr || items != nil {
					t.Fatalf("partial or wrong error: %v %v", items, err)
				}
			}
			if query.Get("page_token") != "" {
				t.Fatal("mutated caller query")
			}
		})
	}
}

func TestConnectionFailureAndTLS(t *testing.T) {
	c, s := newClient(t, func(w http.ResponseWriter, r *http.Request) {})
	s.Close()
	var out json.RawMessage
	var e *Error
	if err := c.Get(context.Background(), "tables", nil, &out); !errors.As(err, &e) || e.Kind != "transport_error" {
		t.Fatalf("expected transport failure: %v", err)
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted certificate accepted") }))
	defer tlsServer.Close()
	c.base, _ = url.Parse(tlsServer.URL + "/api/2.1/unity-catalog/")
	if err := c.Get(context.Background(), "tables", nil, &out); !errors.As(err, &e) || e.Kind != "tls_error" {
		t.Fatalf("expected TLS failure: %v", err)
	}
}

func TestPaginationByteLimit(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page_token") == "" {
			_, _ = w.Write([]byte(`{"items":[1],"next_page_token":"x"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[2]}`))
	})
	c.cfg.MaxListBytes = 8
	items, err := CollectPages[int](context.Background(), c, "items", nil, "items")
	var e *Error
	if !errors.As(err, &e) || e.Kind != "pagination_limit" || items != nil {
		t.Fatalf("expected bounded aggregate with no partial result: %v %v", items, err)
	}
}

func TestEnvironmentToken(t *testing.T) {
	fixture, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-test-token" {
			t.Error("wrong environment token")
		}
		_, _ = w.Write([]byte(`{}`))
	})
	cfg := fixture.cfg
	cfg.UCTokenFile = ""
	cfg.UCToken = "env-test-token"
	client, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var result any
	if err := client.Get(context.Background(), "schemas", nil, &result); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{" ", "bad\ntoken", strings.Repeat("x", 16385)} {
		cfg.UCToken = token
		if _, err := New(cfg, nil); err == nil {
			t.Error("invalid environment token accepted")
		}
	}
}
