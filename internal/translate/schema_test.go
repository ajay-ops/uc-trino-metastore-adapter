package translate

import (
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
	"testing"
)

func TestDatabase(t *testing.T) {
	for _, s := range []unity.SchemaInfo{{Name: "raw", CatalogName: "unity"}, {Name: "curated", CatalogName: "unity", Comment: "description", StorageLocation: "s3://bucket/unchanged%20path/"}} {
		db := Database(s)
		if db.Name != s.Name || db.Description != s.Comment || db.LocationUri != s.StorageLocation || db.Parameters == nil || len(db.Parameters) != 0 || db.OwnerName != nil || db.OwnerType != nil || db.Privileges != nil || db.CatalogName != nil {
			t.Fatalf("unexpected mapping: %+v", db)
		}
	}
}
