package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains environment settings. Never log the configuration or credentials.
type Config struct {
	UCBaseURL           string
	UCCatalog           string
	UCTokenFile         string
	UCToken             string `json:"-"`
	AllowInsecureHTTP   bool
	ThriftAddress       string
	HTTPAddress         string
	ConnectTimeout      time.Duration
	RequestTimeout      time.Duration
	OperationTimeout    time.Duration
	RetryAttempts       int
	RetryInitialBackoff time.Duration
	RetryMaxBackoff     time.Duration
	MaxResponseBytes    int64
	MaxListBytes        int64
	MaxPages            int
	MaxItems            int
	CacheTTL            time.Duration
	CacheMaxEntries     int
	LogLevel            slog.Level
	ShutdownTimeout     time.Duration
	SocketTimeout       time.Duration
	MaxConnections      int
	MaxMessageBytes     int32
}

// Load reads and validates configuration from the process environment.
func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	// Canonical deployment names take precedence, including explicitly empty values.
	original := lookup
	lookup = func(name string) (string, bool) {
		aliases := map[string]string{"THRIFT_ADDRESS": "THRIFT_ADDR", "HTTP_ADDRESS": "HTTP_ADDR", "HTTP_CONNECT_TIMEOUT": "UC_CONNECT_TIMEOUT", "HTTP_REQUEST_TIMEOUT": "UC_REQUEST_TIMEOUT"}
		if canonical, ok := aliases[name]; ok {
			if value, set := original(canonical); set {
				return value, true
			}
		}
		return original(name)
	}
	c := Config{
		ThriftAddress: ":9083", HTTPAddress: ":8080",
		ConnectTimeout: time.Second, RequestTimeout: 3 * time.Second, OperationTimeout: 10 * time.Second,
		RetryAttempts: 2, RetryInitialBackoff: 100 * time.Millisecond, RetryMaxBackoff: time.Second,
		MaxResponseBytes: 4 << 20, MaxListBytes: 16 << 20, MaxPages: 1000, MaxItems: 100000,
		CacheMaxEntries: 1000, ShutdownTimeout: 15 * time.Second, SocketTimeout: 10 * time.Second,
		MaxConnections: 128, MaxMessageBytes: 4 << 20,
	}
	for name, target := range map[string]*string{
		"UC_BASE_URL": &c.UCBaseURL, "UC_CATALOG": &c.UCCatalog, "UC_TOKEN_FILE": &c.UCTokenFile, "UC_TOKEN": &c.UCToken,
		"THRIFT_ADDRESS": &c.ThriftAddress, "HTTP_ADDRESS": &c.HTTPAddress,
	} {
		if v, ok := lookup(name); ok {
			*target = v
		}
	}
	for name, target := range map[string]*time.Duration{
		"HTTP_CONNECT_TIMEOUT": &c.ConnectTimeout, "HTTP_REQUEST_TIMEOUT": &c.RequestTimeout,
		"UC_OPERATION_TIMEOUT": &c.OperationTimeout, "RETRY_INITIAL_BACKOFF": &c.RetryInitialBackoff,
		"RETRY_MAX_BACKOFF": &c.RetryMaxBackoff, "CACHE_TTL": &c.CacheTTL,
		"SHUTDOWN_TIMEOUT": &c.ShutdownTimeout, "THRIFT_SOCKET_TIMEOUT": &c.SocketTimeout,
	} {
		if v, ok := lookup(name); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				return c, fmt.Errorf("%s must be a duration", name)
			}
			*target = d
		}
	}
	for name, target := range map[string]*int{
		"RETRY_MAX_ATTEMPTS": &c.RetryAttempts, "UC_MAX_PAGES": &c.MaxPages,
		"UC_MAX_ITEMS": &c.MaxItems, "CACHE_MAX_ENTRIES": &c.CacheMaxEntries,
		"THRIFT_MAX_CONNECTIONS": &c.MaxConnections,
	} {
		if v, ok := lookup(name); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return c, fmt.Errorf("%s must be an integer", name)
			}
			*target = n
		}
	}
	for name, target := range map[string]*int64{"UC_MAX_RESPONSE_BYTES": &c.MaxResponseBytes, "UC_MAX_LIST_BYTES": &c.MaxListBytes} {
		if v, ok := lookup(name); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return c, fmt.Errorf("%s must be an integer", name)
			}
			*target = n
		}
	}
	if v, ok := lookup("THRIFT_MAX_MESSAGE_BYTES"); ok {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return c, fmt.Errorf("THRIFT_MAX_MESSAGE_BYTES must be a 32-bit integer")
		}
		c.MaxMessageBytes = int32(n)
	}
	if v, ok := lookup("UC_ALLOW_INSECURE_HTTP"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, fmt.Errorf("UC_ALLOW_INSECURE_HTTP must be a boolean")
		}
		c.AllowInsecureHTTP = b
	}
	if v, ok := lookup("LOG_LEVEL"); ok {
		switch strings.ToLower(v) {
		case "debug":
			c.LogLevel = slog.LevelDebug
		case "info":
			c.LogLevel = slog.LevelInfo
		case "warn":
			c.LogLevel = slog.LevelWarn
		case "error":
			c.LogLevel = slog.LevelError
		default:
			return c, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error")
		}
	}
	return c, c.Validate()
}

// Validate rejects unsafe or unsupported combinations before opening listeners.
func (c Config) Validate() error {
	u, err := url.Parse(c.UCBaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("UC_BASE_URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if u.Scheme == "http" && !c.AllowInsecureHTTP {
		return fmt.Errorf("UC_BASE_URL requires HTTPS; UC_ALLOW_INSECURE_HTTP is for isolated local development")
	}
	if strings.TrimRight(u.Path, "/") != "/api/2.1/unity-catalog" {
		return fmt.Errorf("UC_BASE_URL must end in /api/2.1/unity-catalog")
	}
	if c.UCCatalog == "" || strings.TrimSpace(c.UCCatalog) != c.UCCatalog || strings.ContainsAny(c.UCCatalog, "./\\\x00\r\n") {
		return fmt.Errorf("UC_CATALOG must be one non-empty namespace component")
	}
	if (c.UCTokenFile == "") == (c.UCToken == "") {
		return fmt.Errorf("set exactly one of UC_TOKEN or UC_TOKEN_FILE")
	}
	for name, address := range map[string]string{"THRIFT_ADDRESS": c.ThriftAddress, "HTTP_ADDRESS": c.HTTPAddress} {
		_, port, err := net.SplitHostPort(address)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 0 || n > 65535 {
			return fmt.Errorf("%s must be host:port (port 0..65535)", name)
		}
	}
	if c.ConnectTimeout <= 0 || c.RequestTimeout <= 0 || c.OperationTimeout <= 0 || c.ConnectTimeout > c.RequestTimeout || c.RequestTimeout > c.OperationTimeout {
		return fmt.Errorf("timeouts must satisfy 0 < HTTP_CONNECT_TIMEOUT <= HTTP_REQUEST_TIMEOUT <= UC_OPERATION_TIMEOUT")
	}
	if c.RetryAttempts < 1 || c.RetryAttempts > 5 || c.RetryInitialBackoff <= 0 || c.RetryMaxBackoff < c.RetryInitialBackoff || c.RetryMaxBackoff > c.OperationTimeout {
		return fmt.Errorf("retry attempts must be 1..5 and 0 < initial backoff <= maximum backoff <= operation timeout")
	}
	if c.MaxListBytes < 1 || c.MaxListBytes > 64<<20 || c.MaxResponseBytes < 1 || c.MaxResponseBytes > 64<<20 || c.MaxPages < 1 || c.MaxItems < 1 {
		return fmt.Errorf("pagination limits must be positive; response/list byte limits must not exceed 64 MiB")
	}
	if c.CacheTTL != 0 {
		return fmt.Errorf("CACHE_TTL must be 0s: caching is not implemented")
	}
	if c.CacheMaxEntries < 1 {
		return fmt.Errorf("CACHE_MAX_ENTRIES must be positive")
	}
	if c.ShutdownTimeout <= 0 || c.SocketTimeout <= 0 || c.MaxConnections < 1 || c.MaxMessageBytes < 1 || c.MaxMessageBytes > 64<<20 {
		return fmt.Errorf("shutdown, socket and connection limits must be positive; message limit must not exceed 64 MiB")
	}
	return nil
}
