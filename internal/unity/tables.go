package unity

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// TableInfo retains only metadata needed for external Delta discovery. UC columns
// and properties are deliberately not decoded: the Delta log owns the schema.
type TableInfo struct {
	Name             string  `json:"name"`
	CatalogName      string  `json:"catalog_name"`
	SchemaName       string  `json:"schema_name"`
	TableType        string  `json:"table_type"`
	DataSourceFormat string  `json:"data_source_format"`
	StorageLocation  string  `json:"storage_location"`
	Comment          *string `json:"comment"`
	ViewDefinition   string  `json:"view_definition"`
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// eligibleTable validates identity before filtering, so corrupt or cross-catalog
// records cannot disappear as if discovery succeeded. Locations remain unchanged.
func (c *Client) eligibleTable(t TableInfo, schema string) (bool, error) {
	if !validSchemaName(t.Name) || t.SchemaName != schema || t.CatalogName != c.cfg.UCCatalog {
		return false, &Error{Kind: "invalid_response"}
	}
	view := false
	switch t.TableType {
	case "EXTERNAL", "MANAGED":
	case "STREAMING_TABLE", "MATERIALIZED_VIEW", "METRIC_VIEW":
		view = true
	default:
		return false, &Error{Kind: "invalid_response"}
	}
	switch t.DataSourceFormat {
	case "DELTA", "CSV", "JSON", "AVRO", "PARQUET", "ORC", "TEXT":
	case "":
		if !view {
			return false, &Error{Kind: "invalid_response"}
		}
	default:
		return false, &Error{Kind: "invalid_response"}
	}
	if view || t.TableType != "EXTERNAL" || t.DataSourceFormat != "DELTA" {
		return false, nil
	}
	if t.ViewDefinition != "" {
		return false, &Error{Kind: "invalid_response"}
	}
	u, err := url.Parse(t.StorageLocation)
	if err != nil || (u.Scheme != "s3" && u.Scheme != "s3a") || !bucketName.MatchString(u.Host) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(t.StorageLocation, "#") || strings.IndexFunc(t.StorageLocation, unicode.IsControl) != -1 {
		return false, &Error{Kind: "invalid_response"}
	}
	return true, nil
}

// ListTables returns all eligible external Delta tables, with complete bounded
// pagination and no per-table GETs. Only a first-page schema-specific 404 is empty.
func (c *Client) ListTables(ctx context.Context, schema string) ([]TableInfo, error) {
	if !validSchemaName(schema) {
		return nil, &Error{Kind: "invalid_schema_name"}
	}
	tables, err := CollectPages[TableInfo](ctx, c, "tables", url.Values{"catalog_name": {c.cfg.UCCatalog}, "schema_name": {schema}, "max_results": {"50"}}, "tables")
	if err != nil {
		if e, ok := err.(*Error); ok && e.Kind == "http_error" && e.StatusCode == 404 && e.Code == "SCHEMA_NOT_FOUND" {
			return []TableInfo{}, nil
		}
		return nil, err
	}
	result := make([]TableInfo, 0, len(tables))
	seen := make(map[string]bool, len(tables))
	for _, table := range tables {
		eligible, err := c.eligibleTable(table, schema)
		if err != nil {
			return nil, err
		}
		if seen[table.Name] {
			return nil, &Error{Kind: "pagination_duplicate"}
		}
		seen[table.Name] = true
		if eligible {
			result = append(result, table)
		}
	}
	return result, nil
}

// GetTable retrieves one eligible external Delta table in the configured catalog.
func (c *Client) GetTable(ctx context.Context, schema, name string) (*TableInfo, error) {
	if !validSchemaName(schema) {
		return nil, &Error{Kind: "invalid_schema_name"}
	}
	if !validSchemaName(name) {
		return nil, &Error{Kind: "invalid_table_name"}
	}
	var table TableInfo
	if err := c.Get(ctx, "tables/"+url.PathEscape(c.cfg.UCCatalog+"."+schema+"."+name), nil, &table); err != nil {
		return nil, err
	}
	eligible, err := c.eligibleTable(table, schema)
	if err != nil {
		return nil, err
	}
	if table.Name != name {
		return nil, &Error{Kind: "invalid_response"}
	}
	if !eligible {
		return nil, &Error{Kind: "unsupported_table"}
	}
	return &table, nil
}
