package unity

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability"
)

// Error preserves UC's structured error fields without exposing them in logs.
// Message and Code are untrusted upstream data; Error deliberately omits them.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Kind       string
	cause      error
}

func (e *Error) Error() string { return fmt.Sprintf("UC %s (HTTP %d)", e.Kind, e.StatusCode) }
func (e *Error) Unwrap() error { return e.cause }

// Client provides authenticated, bounded GET requests. It has no mutation APIs.
type Client struct {
	base    *url.URL
	http    *http.Client
	cfg     config.Config
	metrics *observability.Metrics
}

// New validates settings and the environment token or readable token file.
// It does not claim the token has been accepted by a live UC server.
func New(cfg config.Config, metrics *observability.Metrics) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if _, err := configuredToken(cfg); err != nil {
		return nil, err
	}
	base, _ := url.Parse(strings.TrimRight(cfg.UCBaseURL, "/") + "/")
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: cfg.ConnectTimeout, ResponseHeaderTimeout: cfg.RequestTimeout,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 32, MaxConnsPerHost: cfg.MaxConnections,
		IdleConnTimeout: 90 * time.Second,
	}
	return &Client{base: base, cfg: cfg, metrics: metrics, http: &http.Client{
		Transport: transport, Timeout: cfg.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Close releases idle upstream connections during shutdown.
func (c *Client) Close() { c.http.CloseIdleConnections() }

// Get decodes a resource-relative GET response within one operation deadline.
// Query parameters must be passed separately; absolute or escaping paths fail.
func (c *Client) Get(ctx context.Context, resource string, query url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.OperationTimeout)
	defer cancel()
	return c.get(ctx, resource, query, out)
}

func (c *Client) get(ctx context.Context, resource string, query url.Values, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rel, err := url.Parse(resource)
	if err != nil || rel.IsAbs() || rel.Host != "" || rel.User != nil || rel.RawQuery != "" || rel.Fragment != "" || strings.HasPrefix(rel.Path, "/") || rel.Path == "" {
		return &Error{Kind: "invalid_resource"}
	}
	for _, part := range strings.Split(rel.Path, "/") {
		if part == ".." || part == "." || strings.ContainsAny(part, "\\\x00\r\n") {
			return &Error{Kind: "invalid_resource"}
		}
	}
	endpoint := c.base.ResolveReference(rel)
	endpoint.RawQuery = query.Encode()
	delay := c.cfg.RetryInitialBackoff
	for attempt := 1; attempt <= c.cfg.RetryAttempts; attempt++ {
		data, retryAfter, err := c.attempt(ctx, endpoint.String())
		if err == nil {
			if err = json.Unmarshal(data, out); err != nil {
				return &Error{Kind: "invalid_response"}
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == c.cfg.RetryAttempts || !retryable(err) {
			return err
		}
		// Equal jitter prevents synchronized retries while keeping a positive delay.
		wait := delay/2 + time.Duration(rand.Int64N(max(1, int64(delay-delay/2))))
		if retryAfter > wait {
			wait = retryAfter
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= wait {
			return err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if c.metrics != nil {
			c.metrics.UCRetries.Inc()
		}
		delay = min(delay*2, c.cfg.RetryMaxBackoff)
	}
	panic("validated retry count must be positive")
}

func (c *Client) attempt(ctx context.Context, endpoint string) ([]byte, time.Duration, error) {
	token, err := configuredToken(c.cfg)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, &Error{Kind: "invalid_resource"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	status := "transport_error"
	defer func() {
		if c.metrics != nil {
			c.metrics.ObserveUC(status, time.Since(started))
		}
	}()
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		var unknown x509.UnknownAuthorityError
		var hostname x509.HostnameError
		var invalid x509.CertificateInvalidError
		if errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
			return nil, 0, &Error{Kind: "tls_error"}
		}
		return nil, 0, &Error{Kind: "transport_error", cause: err}
	}
	defer resp.Body.Close()
	status = strconv.Itoa(resp.StatusCode)
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, &Error{Kind: "transport_error", StatusCode: resp.StatusCode, cause: err}
	}
	if int64(len(data)) > c.cfg.MaxResponseBytes {
		return nil, 0, &Error{Kind: "response_too_large", StatusCode: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		upstream := struct {
			Code    string `json:"error_code"`
			Message string `json:"message"`
		}{}
		// Non-JSON errors (for example proxy errors) remain HTTP failures, never 404 objects.
		_ = json.Unmarshal(data, &upstream)
		return nil, parseRetryAfter(resp.Header.Get("Retry-After")), &Error{StatusCode: resp.StatusCode, Code: upstream.Code, Message: upstream.Message, Kind: "http_error"}
	}
	return data, 0, nil
}

func retryable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	if e.Kind == "transport_error" {
		return true
	}
	return e.Kind == "http_error" && (e.StatusCode == 429 || e.StatusCode == 500 || e.StatusCode == 502 || e.StatusCode == 503 || e.StatusCode == 504)
}

func parseRetryAfter(value string) time.Duration {
	if n, err := strconv.ParseInt(value, 10, 64); err == nil && n >= 0 {
		// A very large hint must exhaust the caller budget, not overflow into a retry.
		if n > 86400 {
			return 24 * time.Hour
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(t))
	}
	return 0
}

// Environment tokens are fixed for the process lifetime; file tokens rotate per attempt.
func configuredToken(cfg config.Config) (string, error) {
	if cfg.UCToken != "" {
		return validateToken([]byte(cfg.UCToken))
	}
	return readToken(cfg.UCTokenFile)
}

func readToken(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", &Error{Kind: "token_unavailable"}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return "", &Error{Kind: "token_unavailable"}
	}
	return validateToken(data)
}

func validateToken(data []byte) (string, error) {
	if len(data) > 16*1024 {
		return "", &Error{Kind: "invalid_token"}
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", &Error{Kind: "invalid_token"}
	}
	return token, nil
}
