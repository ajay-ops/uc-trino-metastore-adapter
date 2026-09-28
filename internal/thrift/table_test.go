package thrift

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
)

func TestTableThriftGolden(t *testing.T) {
	fixture, err := os.ReadFile("../../integration/golden/uc_external_delta.json")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("../../integration/golden/hms_external_delta.expected.json")
	if err != nil {
		t.Fatal(err)
	}
	expected := hms.NewTable()
	if err := json.Unmarshal(golden, expected); err != nil {
		t.Fatal(err)
	}
	listCalls, getCalls := 0, 0
	client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected method/auth")
		}
		switch r.URL.Path {
		case "/api/2.1/unity-catalog/tables":
			listCalls++
			if r.URL.Query().Get("catalog_name") != "configured" || r.URL.Query().Get("schema_name") != "raw" || r.URL.Query().Get("max_results") != "50" {
				t.Error(r.URL)
			}
			switch r.URL.Query().Get("page_token") {
			case "":
				fmt.Fprint(w, `{"tables":[],"next_page_token":"next+page"}`)
			case "next+page":
				fmt.Fprintf(w, `{"tables":[%s]}`, fixture)
			default:
				t.Error(r.URL)
				w.WriteHeader(400)
			}
		case "/api/2.1/unity-catalog/tables/configured.raw.events":
			getCalls++
			_, _ = w.Write(fixture)
		default:
			t.Errorf("unexpected route: %s", r.URL)
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()
	meta, err := client.GetTableMeta(ctx, "raw", "*", []string{})
	if err != nil || len(meta) != 1 || meta[0].TableName != "events" || meta[0].DbName != "raw" || meta[0].TableType != "EXTERNAL_TABLE" || meta[0].Comments == nil || *meta[0].Comments != "Synthetic Delta fixture" || meta[0].CatName != nil {
		t.Fatalf("%v %v", meta, err)
	}
	if listCalls != 2 || getCalls != 0 {
		t.Fatalf("listing performed extra requests: list=%d get=%d", listCalls, getCalls)
	}
	legacy, err := client.GetTable(ctx, "raw", "events")
	if err != nil {
		t.Fatal(err)
	}
	requested, err := client.GetTableReq(ctx, &hms.GetTableRequest{DbName: "raw", TblName: "events", Capabilities: &hms.ClientCapabilities{Values: []hms.ClientCapability{hms.ClientCapability_INSERT_ONLY_TABLES}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy, expected) || !reflect.DeepEqual(requested.Table, expected) {
		t.Fatalf("wire response differs from golden\nlegacy=%+v\nrequest=%+v", legacy, requested.Table)
	}
	for _, types := range [][]string{{"EXTERNAL_TABLE"}, {"VIRTUAL_VIEW", "EXTERNAL_TABLE"}, {"VIRTUAL_VIEW"}, {"MANAGED_TABLE"}} {
		got, err := client.GetTableMeta(ctx, "raw", "*", types)
		want := 0
		for _, kind := range types {
			if kind == "EXTERNAL_TABLE" {
				want = 1
			}
		}
		if err != nil || got == nil || len(got) != want {
			t.Fatalf("types %v: %v %v", types, got, err)
		}
	}
}

func TestTableThriftErrorParity(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		body, code string
		missing    bool
	}{
		{"table-missing", 404, `{"error_code":"TABLE_NOT_FOUND"}`, "", true},
		{"schema-missing", 404, `{"error_code":"SCHEMA_NOT_FOUND"}`, "", true},
		{"catalog-missing", 404, `{"error_code":"CATALOG_NOT_FOUND"}`, "UC_CONFIGURATION_ERROR", false},
		{"generic-missing", 404, `{"error_code":"NOT_FOUND"}`, "UC_CONFIGURATION_ERROR", false},
		{"auth", 401, `{"message":"private-token"}`, "UC_AUTHENTICATION_FAILED", false},
		{"forbidden", 403, `{}`, "UC_AUTHORIZATION_FAILED", false},
		{"throttled", 429, `{}`, "UC_THROTTLED", false},
		{"outage", 503, `{}`, "UC_UNAVAILABLE", false},
		{"timeout", 200, `{}`, "UC_TIMEOUT", false},
		{"malformed", 200, `{`, "UC_INVALID_RESPONSE", false},
		{"managed", 200, `{"name":"events","schema_name":"raw","catalog_name":"configured","table_type":"MANAGED","data_source_format":"DELTA"}`, "UNSUPPORTED_TABLE", false},
		{"non-delta", 200, `{"name":"events","schema_name":"raw","catalog_name":"configured","table_type":"EXTERNAL","data_source_format":"PARQUET"}`, "UNSUPPORTED_TABLE", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.name == "timeout" {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			legacy, e1 := client.GetTable(context.Background(), "raw", "events")
			requested, e2 := client.GetTableReq(context.Background(), &hms.GetTableRequest{DbName: "raw", TblName: "events"})
			if legacy != nil || requested != nil {
				t.Fatal("error returned table")
			}
			for _, err := range []error{e1, e2} {
				if tt.missing {
					var absent *hms.NoSuchObjectException
					if !errors.As(err, &absent) {
						t.Fatalf("expected NoSuchObject: %T %v", err, err)
					}
				} else {
					var meta *hms.MetaException
					if !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, tt.code+":") || strings.Contains(meta.Message, "private-token") {
						t.Fatalf("expected %s: %T %v", tt.code, err, err)
					}
				}
			}
		})
	}
}

func TestTableMetaMissingAndPartial(t *testing.T) {
	for _, mode := range []string{"empty", "schema-missing", "catalog-missing", "outage", "later-404", "later-503", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(mode, "later") || mode == "cycle" {
					if r.URL.Query().Get("page_token") == "" {
						fmt.Fprint(w, `{"tables":[{"name":"events","schema_name":"raw","catalog_name":"configured","table_type":"EXTERNAL","data_source_format":"DELTA","storage_location":"s3://test-bucket/path/"}],"next_page_token":"next"}`)
						return
					}
				}
				switch mode {
				case "empty":
					fmt.Fprint(w, `{"tables":[]}`)
				case "schema-missing", "later-404":
					w.WriteHeader(404)
					fmt.Fprint(w, `{"error_code":"SCHEMA_NOT_FOUND"}`)
				case "catalog-missing":
					w.WriteHeader(404)
					fmt.Fprint(w, `{"error_code":"CATALOG_NOT_FOUND"}`)
				case "outage", "later-503":
					w.WriteHeader(503)
				case "cycle":
					fmt.Fprint(w, `{"tables":[],"next_page_token":"next"}`)
				}
			})
			tables, err := client.GetTableMeta(context.Background(), "raw", "*", []string{})
			if mode == "empty" || mode == "schema-missing" {
				if err != nil || tables == nil || len(tables) != 0 {
					t.Fatalf("%v %v", tables, err)
				}
				return
			}
			want := "UC_LIST_FAILED"
			if mode == "catalog-missing" {
				want = "UC_CONFIGURATION_ERROR"
			}
			if mode == "outage" {
				want = "UC_UNAVAILABLE"
			}
			var meta *hms.MetaException
			if tables != nil || !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, want+":") {
				t.Fatalf("partial/error concealed: %v %v", tables, err)
			}
		})
	}
}

func TestTableRequestRejection(t *testing.T) {
	client := schemaClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached UC") })
	ctx := context.Background()
	for _, catalog := range []string{"configured", "other", ""} {
		_, err := client.GetTableReq(ctx, &hms.GetTableRequest{DbName: "raw", TblName: "events", CatName: &catalog})
		var meta *hms.MetaException
		if !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, "UNSUPPORTED_CATALOG:") {
			t.Fatal(err)
		}
	}
	for _, patterns := range [][2]string{{"*", "*"}, {"raw|other", "*"}, {"raw", ".*"}, {"raw", "events"}, {"raw?", "*"}} {
		_, err := client.GetTableMeta(ctx, patterns[0], patterns[1], []string{})
		var meta *hms.MetaException
		if !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, "UNSUPPORTED_PATTERN:") {
			t.Fatal(err)
		}
	}
	if _, err := (&Handler{}).GetTableReq(ctx, nil); err == nil {
		t.Fatal("accepted nil request")
	}
	_, err := client.GetTable(ctx, "raw", "a.b")
	var meta *hms.MetaException
	if !errors.As(err, &meta) || !strings.HasPrefix(meta.Message, "INVALID_TABLE_NAME:") {
		t.Fatal(err)
	}
}
