package unity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaPagination(t *testing.T) {
	for _, failure := range []string{"", "duplicate", "wrong-catalog", "wrong-full-name", "missing-name", "second-page-outage"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/api/2.1/unity-catalog/schemas" || r.URL.Query().Get("catalog_name") != "unity" || r.URL.Query().Get("max_results") != "100" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("wrong request: %s", r.URL)
				}
				switch r.URL.Query().Get("page_token") {
				case "":
					fmt.Fprint(w, `{"schemas":[{"name":"raw","catalog_name":"unity","full_name":"unity.raw"}],"next_page_token":"a+b & c"}`)
				case "a+b & c":
					if failure == "second-page-outage" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"schemas":[],"next_page_token":"last"}`)
				case "last":
					name, catalog, full := "curated", "unity", "unity.curated"
					switch failure {
					case "duplicate":
						name, full = "raw", "unity.raw"
					case "wrong-catalog":
						catalog = "other"
					case "wrong-full-name":
						full = "other.curated"
					case "missing-name":
						name = ""
					}
					fmt.Fprintf(w, `{"schemas":[{"name":%q,"catalog_name":%q,"full_name":%q}]}`, name, catalog, full)
				default:
					t.Errorf("bad page token: %s", r.URL)
					w.WriteHeader(400)
				}
			})
			schemas, err := c.ListSchemas(context.Background())
			if failure != "" {
				if err == nil || schemas != nil {
					t.Fatalf("partial success: %v %v", schemas, err)
				}
				return
			}
			if err != nil || len(schemas) != 2 || schemas[0].Name != "raw" || schemas[1].Name != "curated" || calls != 3 {
				t.Fatalf("%v %v calls=%d", schemas, err, calls)
			}
		})
	}
}

func TestGetSchemaIdentityAndEncoding(t *testing.T) {
	for _, name := range []string{"raw", "percent%name", "plus+name", "quoted?name", "CaseSensitive", "unicode_é"} {
		t.Run(name, func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/2.1/unity-catalog/schemas/unity."+name || r.URL.RawQuery != "" {
					t.Errorf("wrong URL: %s", r.URL)
				}
				fmt.Fprintf(w, `{"name":%q,"catalog_name":"unity","comment":"hello","storage_location":"s3://bucket/path/","owner":"ignored","properties":{"x":"ignored"}}`, name)
			})
			got, err := c.GetSchema(context.Background(), name)
			want := &SchemaInfo{Name: name, CatalogName: "unity", Comment: "hello", StorageLocation: "s3://bucket/path/"}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	for _, response := range []string{`null`, `{}`, `{"name":"other","catalog_name":"unity"}`, `{"name":"raw","catalog_name":"other"}`, `{"name":"raw","catalog_name":"unity","full_name":"unity.other"}`} {
		t.Run(response, func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) })
			got, err := c.GetSchema(context.Background(), "raw")
			var e *Error
			if got != nil || !errors.As(err, &e) || e.Kind != "invalid_response" {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestInvalidSchemaNameNeverRequestsUC(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid name reached UC") })
	for _, name := range []string{"", " raw", "raw ", "a.b", "a/b", "a\\b", "a\n", "@catalog#raw", "!", "a b"} {
		if _, err := c.GetSchema(context.Background(), name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestSchema404PreservesCode(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error_code":"SCHEMA_NOT_FOUND","message":"private upstream details"}`)
	})
	_, err := c.GetSchema(context.Background(), "missing")
	var e *Error
	if !errors.As(err, &e) || e.StatusCode != 404 || e.Code != "SCHEMA_NOT_FOUND" || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}
