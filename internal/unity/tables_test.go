package unity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func deltaTable(name string) TableInfo {
	return TableInfo{Name: name, CatalogName: "unity", SchemaName: "raw", TableType: "EXTERNAL", DataSourceFormat: "DELTA", StorageLocation: "s3://test-bucket/raw/" + name + "/"}
}

func TestTablePaginationAndScope(t *testing.T) {
	calls := 0
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/2.1/unity-catalog/tables" || r.URL.Query().Get("catalog_name") != "unity" || r.URL.Query().Get("schema_name") != "raw" || r.URL.Query().Get("max_results") != "50" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("wrong request: %s", r.URL)
		}
		page := []TableInfo{}
		next := ""
		switch r.URL.Query().Get("page_token") {
		case "":
			for i := 0; i < 50; i++ {
				page = append(page, deltaTable(fmt.Sprintf("t%02d", i)))
			}
			next = "empty+ & page"
		case "empty+ & page":
			next = "last"
		case "last":
			page = append(page, deltaTable("t50"))
			managed := deltaTable("managed")
			managed.TableType = "MANAGED"
			parquet := deltaTable("parquet")
			parquet.DataSourceFormat = "PARQUET"
			view := deltaTable("view")
			view.TableType = "METRIC_VIEW"
			view.DataSourceFormat = ""
			view.StorageLocation = ""
			page = append(page, managed, parquet, view)
		default:
			t.Error("unexpected token")
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tables": page, "next_page_token": next})
	})
	c.cfg.MaxResponseBytes = 1 << 20
	c.cfg.MaxListBytes = 1 << 20
	c.cfg.MaxItems = 100
	result, err := c.ListTables(context.Background(), "raw")
	if err != nil || len(result) != 51 || calls != 3 {
		t.Fatalf("count=%d calls=%d err=%v", len(result), calls, err)
	}
	for i, item := range result {
		if item.Name != fmt.Sprintf("t%02d", i) {
			t.Fatal(item)
		}
	}
}

func TestTableValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*TableInfo)
		kind   string
	}{
		{"valid", func(*TableInfo) {}, ""},
		{"managed", func(x *TableInfo) { x.TableType = "MANAGED" }, "unsupported_table"},
		{"parquet", func(x *TableInfo) { x.DataSourceFormat = "PARQUET" }, "unsupported_table"},
		{"view", func(x *TableInfo) { x.TableType = "MATERIALIZED_VIEW"; x.DataSourceFormat = ""; x.StorageLocation = "" }, "unsupported_table"},
		{"streaming", func(x *TableInfo) { x.TableType = "STREAMING_TABLE" }, "unsupported_table"},
		{"missing-name", func(x *TableInfo) { x.Name = "" }, "invalid_response"},
		{"wrong-name", func(x *TableInfo) { x.Name = "other" }, "invalid_response"},
		{"wrong-schema", func(x *TableInfo) { x.SchemaName = "other" }, "invalid_response"},
		{"wrong-catalog", func(x *TableInfo) { x.CatalogName = "other" }, "invalid_response"},
		{"missing-type", func(x *TableInfo) { x.TableType = "" }, "invalid_response"},
		{"unknown-type", func(x *TableInfo) { x.TableType = "FUTURE" }, "invalid_response"},
		{"missing-format", func(x *TableInfo) { x.DataSourceFormat = "" }, "invalid_response"},
		{"unknown-format", func(x *TableInfo) { x.DataSourceFormat = "FUTURE" }, "invalid_response"},
		{"fake-view", func(x *TableInfo) { x.ViewDefinition = "SELECT secret" }, "invalid_response"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := deltaTable("events")
			tt.mutate(&input)
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/2.1/unity-catalog/tables" {
					_ = json.NewEncoder(w).Encode(map[string]any{"tables": []TableInfo{input}})
				} else {
					_ = json.NewEncoder(w).Encode(input)
				}
			})
			got, err := c.GetTable(context.Background(), "raw", "events")
			var e *Error
			if tt.kind == "" {
				if err != nil || got == nil {
					t.Fatal(err)
				}
			} else if got != nil || !errors.As(err, &e) || e.Kind != tt.kind {
				t.Fatalf("%v %v", got, err)
			}
			list, err := c.ListTables(context.Background(), "raw")
			if tt.kind == "unsupported_table" {
				if err != nil || list == nil || len(list) != 0 {
					t.Fatalf("ineligible list: %v %v", list, err)
				}
			} else if tt.kind == "invalid_response" && tt.name != "wrong-name" {
				if list != nil || err == nil {
					t.Fatalf("invalid metadata concealed: %v %v", list, err)
				}
			} else if err != nil || len(list) != 1 {
				t.Fatalf("%v %v", list, err)
			}
		})
	}
}

func TestTableLocations(t *testing.T) {
	for _, location := range []string{"s3://test-bucket/a b/%20/", "s3a://test-bucket/prefix//", "s3://test-bucket", "", "file:///tmp/table", "https://test-bucket/table", "s3:///missing-bucket", "s3://user:secret@test-bucket/path", "s3://test-bucket:443/path", "s3://test-bucket/path?token=secret", "s3://test-bucket/path#fragment", "s3://test-bucket/path?", "s3://test-bucket/path#", "s3://test-bucket/path\n", "s3://test-bucket/%invalid"} {
		t.Run(location, func(t *testing.T) {
			source := deltaTable("t")
			source.StorageLocation = location
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(source) })
			table, err := c.GetTable(context.Background(), "raw", "t")
			valid := location == "s3://test-bucket/a b/%20/" || location == "s3a://test-bucket/prefix//" || location == "s3://test-bucket"
			if valid {
				if err != nil || table.StorageLocation != location {
					t.Fatalf("rewritten/rejected location: %v %v", table, err)
				}
			} else if err == nil || table != nil {
				t.Fatalf("accepted %q", location)
			}
		})
	}
}

func TestTablePaginationFailures(t *testing.T) {
	for _, failure := range []string{"duplicate", "404", "503", "cycle"} {
		t.Run(failure, func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page_token") == "" {
					_ = json.NewEncoder(w).Encode(map[string]any{"tables": []TableInfo{deltaTable("first")}, "next_page_token": "next"})
					return
				}
				switch failure {
				case "404":
					w.WriteHeader(404)
					fmt.Fprint(w, `{"error_code":"SCHEMA_NOT_FOUND"}`)
				case "503":
					w.WriteHeader(503)
				case "duplicate":
					_ = json.NewEncoder(w).Encode(map[string]any{"tables": []TableInfo{deltaTable("first")}})
				case "cycle":
					fmt.Fprint(w, `{"tables":[],"next_page_token":"next"}`)
				}
			})
			tables, err := c.ListTables(context.Background(), "raw")
			if tables != nil || err == nil {
				t.Fatalf("failure concealed: %v %v", tables, err)
			}
		})
	}
	for _, code := range []string{"SCHEMA_NOT_FOUND", "CATALOG_NOT_FOUND", "NOT_FOUND", "TABLE_NOT_FOUND"} {
		t.Run(code, func(t *testing.T) {
			c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(404)
				fmt.Fprintf(w, `{"error_code":%q}`, code)
			})
			tables, err := c.ListTables(context.Background(), "raw")
			if code == "SCHEMA_NOT_FOUND" {
				if tables == nil || len(tables) != 0 || err != nil {
					t.Fatalf("%v %v", tables, err)
				}
			} else if err == nil || tables != nil {
				t.Fatalf("configuration error concealed: %v %v", tables, err)
			}
		})
	}
}

func TestTableNamesAndPathEscaping(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/2.1/unity-catalog/tables/unity.raw.t+%?" || r.URL.RawQuery != "" {
			t.Errorf("wrong path: %s", r.URL)
		}
		table := deltaTable("t+%?")
		table.StorageLocation = "s3://test-bucket/path/"
		_ = json.NewEncoder(w).Encode(table)
	})
	if _, err := c.GetTable(context.Background(), "raw", "t+%?"); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][2]string{{"raw.bad", "t"}, {"raw", ""}, {"raw", "a/b"}, {"@hive#raw", "t"}} {
		if _, err := c.GetTable(context.Background(), names[0], names[1]); err == nil {
			t.Fatal(names)
		}
	}
}
