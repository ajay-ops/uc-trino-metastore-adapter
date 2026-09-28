package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/translate"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
	apache "github.com/apache/thrift/lib/go/thrift"
)

// Decode by upstream field IDs rather than the generated Table reader, checking
// that empty containers really exist on the wire and that Trino's path survives.
func TestDeltaTableWireShape(t *testing.T) {
	data, err := os.ReadFile("golden/uc_external_delta.json")
	if err != nil {
		t.Fatal(err)
	}
	var source unity.TableInfo
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	buffer := apache.NewTMemoryBufferLen(1024)
	protocol := apache.NewTBinaryProtocolFactoryDefault().GetProtocol(buffer)
	if err := translate.Table(source).Write(ctx, protocol); err != nil {
		t.Fatal(err)
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var walk func(string)
	walk = func(structure string) {
		_, err := protocol.ReadStructBegin(ctx)
		check(err)
		seen := map[int16]bool{}
		for {
			_, kind, id, err := protocol.ReadFieldBegin(ctx)
			check(err)
			if kind == apache.STOP {
				break
			}
			seen[id] = true
			switch {
			case structure == "table" && id == 7:
				if kind != apache.STRUCT {
					t.Fatal("missing storage descriptor")
				}
				walk("storage")
			case structure == "storage" && id == 7:
				if kind != apache.STRUCT {
					t.Fatal("missing SerDe")
				}
				walk("serde")
			case structure == "table" && id == 8, structure == "storage" && (id == 1 || id == 8 || id == 9):
				if kind != apache.LIST {
					t.Fatal("expected list")
				}
				element, size, err := protocol.ReadListBegin(ctx)
				check(err)
				expected := apache.TType(apache.STRUCT)
				if structure == "storage" && id == 8 {
					expected = apache.STRING
				}
				if size != 0 || element != expected {
					t.Fatalf("%s field %d: size=%d type=%v", structure, id, size, element)
				}
				check(protocol.ReadListEnd(ctx))
			case structure == "table" && id == 9, structure == "serde" && id == 3, structure == "storage" && id == 10:
				if kind != apache.MAP {
					t.Fatal("expected map")
				}
				key, value, size, err := protocol.ReadMapBegin(ctx)
				check(err)
				if key != apache.STRING || value != apache.STRING {
					t.Fatal("wrong map types")
				}
				if structure == "storage" {
					if size != 0 {
						t.Fatal("unexpected SD parameters")
					}
				} else {
					if size != 1 {
						t.Fatal("wrong parameter count")
					}
					k, err := protocol.ReadString(ctx)
					check(err)
					v, err := protocol.ReadString(ctx)
					check(err)
					if structure == "table" {
						if k != "spark.sql.sources.provider" || v != "DELTA" {
							t.Fatal(k, v)
						}
					} else if k != "path" || v != source.StorageLocation {
						t.Fatal(k, v)
					}
				}
				check(protocol.ReadMapEnd(ctx))
			case structure == "storage" && id == 2:
				if kind != apache.STRING {
					t.Fatal("location type")
				}
				location, err := protocol.ReadString(ctx)
				check(err)
				if location != source.StorageLocation {
					t.Fatal("rewritten location")
				}
			default:
				check(protocol.Skip(ctx, kind))
			}
			check(protocol.ReadFieldEnd(ctx))
		}
		check(protocol.ReadStructEnd(ctx))
		required := map[string][]int16{"table": {7, 8, 9}, "storage": {1, 2, 7, 8, 9, 10}, "serde": {3}}
		for _, id := range required[structure] {
			if !seen[id] {
				t.Fatalf("%s field %d absent", structure, id)
			}
		}
	}
	walk("table")
}
