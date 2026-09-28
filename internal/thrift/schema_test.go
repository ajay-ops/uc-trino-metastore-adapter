package thrift

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability"
	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
)

func schemaClient(t *testing.T, endpoint http.HandlerFunc) *hms.ThriftHiveMetastoreClient {
	t.Helper()
	upstream := httptest.NewServer(endpoint)
	t.Cleanup(upstream.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{UCBaseURL: upstream.URL + "/api/2.1/unity-catalog", UCCatalog: "configured", UCTokenFile: token, AllowInsecureHTTP: true,
		ThriftAddress: "127.0.0.1:0", HTTPAddress: ":0", ConnectTimeout: 50 * time.Millisecond, RequestTimeout: 100 * time.Millisecond, OperationTimeout: time.Second,
		RetryAttempts: 1, RetryInitialBackoff: time.Millisecond, RetryMaxBackoff: time.Millisecond, MaxResponseBytes: 4096, MaxListBytes: 16384, MaxPages: 5, MaxItems: 10, CacheMaxEntries: 10,
		ShutdownTimeout: time.Second, SocketTimeout: time.Second, MaxConnections: 4, MaxMessageBytes: 4096}
	uc, err := unity.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uc.Close)
	server, err := New(cfg, observability.NewLogger(io.Discard, 0), observability.NewMetrics(), uc)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-done
	})
	_, client := connect(t, server.Addr().String())
	return client
}

func TestSchemaThriftSuccess(t *testing.T) {
	client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("wrong method/auth")
		}
		switch r.URL.Path {
		case "/api/2.1/unity-catalog/schemas":
			if r.URL.Query().Get("catalog_name") != "configured" || r.URL.Query().Get("max_results") != "100" {
				t.Error("wrong catalog/page size")
			}
			switch r.URL.Query().Get("page_token") {
			case "":
				fmt.Fprint(w, `{"schemas":[{"name":"raw","catalog_name":"configured"}],"next_page_token":"empty"}`)
			case "empty":
				fmt.Fprint(w, `{"schemas":[],"next_page_token":"last"}`)
			case "last":
				fmt.Fprint(w, `{"schemas":[{"name":"curated","catalog_name":"configured"}]}`)
			default:
				t.Error("bad token")
				w.WriteHeader(400)
			}
		case "/api/2.1/unity-catalog/schemas/configured.raw":
			fmt.Fprint(w, `{"name":"raw","catalog_name":"configured","comment":"raw schema","storage_location":"s3://bucket/raw/","owner":"ignored","properties":{"ignore":"me"}}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
		}
	})
	names, err := client.GetAllDatabases(context.Background())
	if err != nil || !reflect.DeepEqual(names, []string{"raw", "curated"}) {
		t.Fatalf("%v %v", names, err)
	}
	db, err := client.GetDatabase(context.Background(), "raw")
	if err != nil {
		t.Fatal(err)
	}
	if db.Name != "raw" || db.Description != "raw schema" || db.LocationUri != "s3://bucket/raw/" || db.Parameters == nil || len(db.Parameters) != 0 || db.OwnerName != nil || db.OwnerType != nil || db.Privileges != nil || db.CatalogName != nil {
		t.Fatalf("wire mapping: %+v", db)
	}

}

func TestSchemaThriftFailures(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		code    string
		missing bool
	}{
		{"schema404", 404, `{"error_code":"SCHEMA_NOT_FOUND"}`, "", true},
		{"catalog404", 404, `{"error_code":"CATALOG_NOT_FOUND"}`, "UC_CONFIGURATION_ERROR", false},
		{"generic404", 404, `{"error_code":"NOT_FOUND"}`, "UC_CONFIGURATION_ERROR", false},
		{"proxy404", 404, `<html>missing</html>`, "UC_CONFIGURATION_ERROR", false},
		{"auth", 401, `{}`, "UC_AUTHENTICATION_FAILED", false},
		{"denied", 403, `{}`, "UC_AUTHORIZATION_FAILED", false},
		{"conflict", 409, `{}`, "UC_CONFLICT", false},
		{"throttled", 429, `{}`, "UC_THROTTLED", false},
		{"outage", 503, `{"message":"secret"}`, "UC_UNAVAILABLE", false},
		{"timeout", 200, `{}`, "UC_TIMEOUT", false},
		{"invalid", 200, `{broken`, "UC_INVALID_RESPONSE", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.name == "timeout" {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			db, err := client.GetDatabase(context.Background(), "missing")
			if db != nil {
				t.Fatal("error returned database")
			}
			var absent *hms.NoSuchObjectException
			var meta *hms.MetaException
			if tt.missing {
				if !errors.As(err, &absent) {
					t.Fatalf("expected NoSuchObject: %T %v", err, err)
				}
			} else if !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, tt.code+":") {
				t.Fatalf("expected %s: %T %v", tt.code, err, err)
			}
			names, err := client.GetAllDatabases(context.Background())
			code := tt.code
			if tt.missing {
				code = "UC_CONFIGURATION_ERROR"
			}
			if names != nil || !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, code+":") {
				t.Fatalf("list failure concealed: %v %v", names, err)
			}
		})
	}
}

func TestSchemaThriftEmptyAndPartial(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) {
				if !failure {
					fmt.Fprint(w, `{"schemas":[]}`)
					return
				}
				if r.URL.Query().Get("page_token") == "" {
					fmt.Fprint(w, `{"schemas":[{"name":"raw","catalog_name":"configured"}],"next_page_token":"next"}`)
					return
				}
				w.WriteHeader(503)
			})
			names, err := client.GetAllDatabases(context.Background())
			if failure {
				var meta *hms.MetaException
				if names != nil || !errors.As(err, &meta) {
					t.Fatalf("partial success: %v %v", names, err)
				}
			} else if err != nil || names == nil || len(names) != 0 {
				t.Fatalf("empty list wire encoding: %v %v", names, err)
			}
		})
	}
}

func TestSchemaErrorCategories(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code string
	}{
		{context.Canceled, "UC_CANCELED"},
		{context.DeadlineExceeded, "UC_TIMEOUT"},
		{&unity.Error{Kind: "invalid_schema_name"}, "INVALID_SCHEMA_NAME"},
		{&unity.Error{Kind: "response_too_large"}, "UC_INVALID_RESPONSE"},
		{&unity.Error{Kind: "pagination_cycle"}, "UC_LIST_FAILED"},
		{&unity.Error{Kind: "pagination_limit"}, "UC_LIST_FAILED"},
		{&unity.Error{Kind: "pagination_duplicate"}, "UC_LIST_FAILED"},
		{&unity.Error{Kind: "token_unavailable"}, "UC_AUTHENTICATION_FAILED"},
		{&unity.Error{Kind: "invalid_token"}, "UC_AUTHENTICATION_FAILED"},
		{&unity.Error{Kind: "tls_error"}, "UC_CONFIGURATION_ERROR"},
	} {
		t.Run(tt.code+tt.err.Error(), func(t *testing.T) {
			var meta *hms.MetaException
			if err := metadataError(tt.err, "schema"); !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, tt.code+":") {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidSchemaThriftRequest(t *testing.T) {
	client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid schema request reached UC") })
	db, err := client.GetDatabase(context.Background(), "@other#raw")
	var meta *hms.MetaException
	if db != nil || !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, "INVALID_SCHEMA_NAME:") {
		t.Fatalf("%v %v", db, err)
	}
}
