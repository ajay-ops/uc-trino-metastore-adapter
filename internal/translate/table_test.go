package translate

import (
	"encoding/json"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
	"os"
	"reflect"
	"testing"
)

func TestTableGolden(t *testing.T) {
	source, err := os.ReadFile("../../integration/golden/uc_external_delta.json")
	if err != nil {
		t.Fatal(err)
	}
	var info unity.TableInfo
	if err := json.Unmarshal(source, &info); err != nil {
		t.Fatal(err)
	}
	table := Table(info)
	actual, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("../../integration/golden/hms_external_delta.expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("golden mismatch\ngot: %s\nwant: %s", actual, golden)
	}
	meta := TableMeta(info)
	if meta.TableType != "EXTERNAL_TABLE" || meta.TableName != info.Name || meta.DbName != info.SchemaName || meta.Comments == nil || *meta.Comments != *info.Comment || meta.CatName != nil {
		t.Fatalf("meta: %+v", meta)
	}
	info.Comment = nil
	if TableMeta(info).Comments != nil {
		t.Fatal("fabricated comment")
	}
	// Separate responses must not share mutable maps/lists.
	table.Parameters["spark.sql.sources.provider"] = "mutated"
	table.Sd.SerdeInfo.Parameters["path"] = "mutated"
	second := Table(info)
	if second.Parameters["spark.sql.sources.provider"] != "DELTA" || second.Sd.SerdeInfo.Parameters["path"] != info.StorageLocation {
		t.Fatal("shared mutable metadata")
	}
}
