package translate

import (
	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
)

// Table maps a validated external Delta table to the minimal Trino HMS contract.
// Empty columns/partition keys leave schema resolution entirely to the Delta log.
func Table(source unity.TableInfo) *hms.Table {
	table := hms.NewTable()
	table.TableName = source.Name
	table.DbName = source.SchemaName
	table.TableType = "EXTERNAL_TABLE"
	table.Parameters = map[string]string{"spark.sql.sources.provider": "DELTA"}
	table.PartitionKeys = []*hms.FieldSchema{}
	table.Sd = &hms.StorageDescriptor{
		Cols: []*hms.FieldSchema{}, Location: source.StorageLocation,
		SerdeInfo:  &hms.SerDeInfo{Name: source.Name, Parameters: map[string]string{"path": source.StorageLocation}},
		BucketCols: []string{}, SortCols: []*hms.Order{}, Parameters: map[string]string{},
	}
	return table
}

// TableMeta describes only eligible external Delta tables for Trino discovery.
func TableMeta(source unity.TableInfo) *hms.TableMeta {
	return &hms.TableMeta{DbName: source.SchemaName, TableName: source.Name, TableType: "EXTERNAL_TABLE", Comments: source.Comment}
}
