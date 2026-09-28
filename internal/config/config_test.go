package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	base := map[string]string{"UC_BASE_URL": "https://uc.example/api/2.1/unity-catalog", "UC_CATALOG": "unity", "UC_TOKEN_FILE": "/run/secrets/token"}
	read := func(values map[string]string) (Config, error) {
		return load(func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	}
	c, err := read(base)
	if err != nil {
		t.Fatal(err)
	}
	if c.ThriftAddress != ":9083" || c.HTTPAddress != ":8080" || c.RequestTimeout != 3*time.Second || c.RetryAttempts != 2 || c.CacheTTL != 0 {
		t.Fatalf("incorrect defaults: %+v", c)
	}
	cases := []struct{ name, value string }{
		{"UC_BASE_URL", ""}, {"UC_BASE_URL", "http://uc/api/2.1/unity-catalog"}, {"UC_BASE_URL", "https://user:secret@uc/api/2.1/unity-catalog"}, {"UC_BASE_URL", "https://uc/wrong"},
		{"UC_CATALOG", ""}, {"UC_CATALOG", "a.b"}, {"UC_TOKEN_FILE", ""},
		{"THRIFT_ADDRESS", "bad"}, {"HTTP_ADDRESS", ":65536"}, {"HTTP_ADDRESS", ":-1"},
		{"HTTP_CONNECT_TIMEOUT", "0s"}, {"HTTP_REQUEST_TIMEOUT", "500ms"}, {"UC_OPERATION_TIMEOUT", "1s"}, {"HTTP_REQUEST_TIMEOUT", "garbage"},
		{"RETRY_MAX_ATTEMPTS", "0"}, {"RETRY_MAX_ATTEMPTS", "6"}, {"RETRY_MAX_ATTEMPTS", "x"}, {"RETRY_INITIAL_BACKOFF", "0s"}, {"RETRY_MAX_BACKOFF", "1ms"},
		{"CACHE_TTL", "1s"}, {"CACHE_TTL", "-1s"}, {"CACHE_MAX_ENTRIES", "0"}, {"UC_MAX_RESPONSE_BYTES", "0"}, {"UC_MAX_RESPONSE_BYTES", "999999999"},
		{"UC_MAX_PAGES", "0"}, {"UC_MAX_ITEMS", "-1"}, {"LOG_LEVEL", "trace"}, {"UC_ALLOW_INSECURE_HTTP", "sometimes"},
		{"THRIFT_MAX_MESSAGE_BYTES", "2147483648"}, {"THRIFT_MAX_MESSAGE_BYTES", "0"}, {"THRIFT_MAX_CONNECTIONS", "0"}, {"THRIFT_SOCKET_TIMEOUT", "0s"}, {"SHUTDOWN_TIMEOUT", "0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			values := map[string]string{}
			for k, v := range base {
				values[k] = v
			}
			values[tc.name] = tc.value
			if _, err := read(values); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
	base["UC_BASE_URL"] = "http://localhost:8080/api/2.1/unity-catalog/"
	base["UC_ALLOW_INSECURE_HTTP"] = "true"
	base["LOG_LEVEL"] = "debug"
	c, err = read(base)
	if err != nil || c.LogLevel != slog.LevelDebug {
		t.Fatalf("local settings: %v", err)
	}
}

func TestDeploymentEnvironment(t *testing.T) {
	values := map[string]string{"UC_BASE_URL": "https://uc.example/api/2.1/unity-catalog", "UC_CATALOG": "unity", "UC_TOKEN": "test-token",
		"THRIFT_ADDR": "127.0.0.1:9083", "HTTP_ADDR": "127.0.0.1:8080", "UC_CONNECT_TIMEOUT": "200ms", "UC_REQUEST_TIMEOUT": "2s",
		"THRIFT_ADDRESS": "invalid-legacy", "HTTP_ADDRESS": "invalid-legacy", "HTTP_CONNECT_TIMEOUT": "invalid-legacy", "HTTP_REQUEST_TIMEOUT": "invalid-legacy"}
	read := func() (Config, error) {
		return load(func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	}
	c, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if c.UCToken != "test-token" || c.ThriftAddress != "127.0.0.1:9083" || c.HTTPAddress != "127.0.0.1:8080" || c.ConnectTimeout != 200*time.Millisecond || c.RequestTimeout != 2*time.Second {
		t.Fatal("canonical settings were not applied")
	}
	for _, key := range []string{"THRIFT_ADDR", "HTTP_ADDR", "UC_CONNECT_TIMEOUT", "UC_REQUEST_TIMEOUT"} {
		saved := values[key]
		values[key] = ""
		if _, err := read(); err == nil {
			t.Errorf("empty canonical %s accepted", key)
		}
		values[key] = saved
	}
	values["UC_TOKEN_FILE"] = "/some/token"
	if _, err := read(); err == nil {
		t.Fatal("ambiguous credentials accepted")
	}
	delete(values, "UC_TOKEN_FILE")
	delete(values, "UC_TOKEN")
	if _, err := read(); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
