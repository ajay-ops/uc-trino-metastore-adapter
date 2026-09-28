package translate

import (
	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
)

// Database maps a validated UC schema to the read-only HMS database contract.
// UC owners/properties are not HMS grants; absent locations remain empty.
func Database(schema unity.SchemaInfo) *hms.Database {
	return &hms.Database{Name: schema.Name, Description: schema.Comment, LocationUri: schema.StorageLocation, Parameters: map[string]string{}}
}
