package thrift

import (
	"context"
	"errors"
	"net"
	"strings"

	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/translate"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
)

// Handler implements the five approved read-only metadata RPCs.
type Handler struct{ UC *unity.Client }

func (h *Handler) GetAllDatabases(ctx context.Context) ([]string, error) {
	schemas, err := h.UC.ListSchemas(ctx)
	if err != nil {
		return nil, metadataError(err, "")
	}
	names := make([]string, len(schemas))
	for i, schema := range schemas {
		names[i] = schema.Name
	}
	return names, nil
}
func (h *Handler) GetDatabase(ctx context.Context, name string) (*hms.Database, error) {
	schema, err := h.UC.GetSchema(ctx, name)
	if err != nil {
		return nil, metadataError(err, "schema")
	}
	return translate.Database(*schema), nil
}

// Only matching object-specific 404s prove absence. Catalog/proxy/route 404s fail closed.
func metadataError(err error, object string) error {
	var upstream *unity.Error
	var timeout net.Error
	code := "UC_UNAVAILABLE"
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		code = "UC_TIMEOUT"
	case errors.Is(err, context.Canceled):
		code = "UC_CANCELED"
	case errors.As(err, &upstream):
		if upstream.StatusCode == 404 && ((object == "schema" && upstream.Code == "SCHEMA_NOT_FOUND") || (object == "table" && (upstream.Code == "TABLE_NOT_FOUND" || upstream.Code == "SCHEMA_NOT_FOUND"))) {
			return &hms.NoSuchObjectException{Message: "UC_" + strings.ToUpper(object) + "_NOT_FOUND: requested object does not exist"}
		}
		switch upstream.Kind {
		case "invalid_table_name":
			code = "INVALID_TABLE_NAME"
		case "unsupported_table":
			code = "UNSUPPORTED_TABLE"
		case "invalid_schema_name":
			code = "INVALID_SCHEMA_NAME"
		case "invalid_response", "response_too_large":
			code = "UC_INVALID_RESPONSE"
		case "pagination_limit", "pagination_cycle", "pagination_duplicate", "pagination_failed":
			code = "UC_LIST_FAILED"
		case "token_unavailable", "invalid_token":
			code = "UC_AUTHENTICATION_FAILED"
		case "tls_error":
			code = "UC_CONFIGURATION_ERROR"
		default:
			switch upstream.StatusCode {
			case 401:
				code = "UC_AUTHENTICATION_FAILED"
			case 403:
				code = "UC_AUTHORIZATION_FAILED"
			case 404:
				code = "UC_CONFIGURATION_ERROR"
			case 409:
				code = "UC_CONFLICT"
			case 429:
				code = "UC_THROTTLED"
			}
		}
	}
	return &hms.MetaException{Message: code + ": metadata read failed"}
}

// GetTableReq shares lookup/translation with the legacy compatibility method.
func (h *Handler) GetTableReq(ctx context.Context, req *hms.GetTableRequest) (*hms.GetTableResult_, error) {
	if req == nil {
		return nil, &hms.MetaException{Message: "INVALID_REQUEST: missing table request"}
	}
	// HMS catalog semantics are intentionally disabled; UC_CATALOG is the mapping.
	if req.CatName != nil {
		return nil, &hms.MetaException{Message: "UNSUPPORTED_CATALOG: leave HMS catalog-name unset"}
	}
	table, err := h.GetTable(ctx, req.DbName, req.TblName)
	if err != nil {
		return nil, err
	}
	return &hms.GetTableResult_{Table: table}, nil
}
func (h *Handler) GetTable(ctx context.Context, schema, name string) (*hms.Table, error) {
	table, err := h.UC.GetTable(ctx, schema, name)
	if err != nil {
		return nil, metadataError(err, "table")
	}
	return translate.Table(*table), nil
}
func (h *Handler) GetTableMeta(ctx context.Context, schema, pattern string, types []string) ([]*hms.TableMeta, error) {
	// The pinned TCP client sends an exact ordinary schema, table '*', and no types.
	if strings.ContainsAny(schema, "*|?") || pattern != "*" {
		return nil, &hms.MetaException{Message: "UNSUPPORTED_PATTERN: expected exact schema and table '*'"}
	}
	tables, err := h.UC.ListTables(ctx, schema)
	if err != nil {
		return nil, metadataError(err, "")
	}
	include := len(types) == 0
	for _, kind := range types {
		if kind == "EXTERNAL_TABLE" {
			include = true
		}
	}
	result := make([]*hms.TableMeta, 0, len(tables))
	if include {
		for _, table := range tables {
			result = append(result, translate.TableMeta(table))
		}
	}
	return result, nil
}
