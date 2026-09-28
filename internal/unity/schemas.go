package unity

import (
	"context"
	"net/url"
	"strings"
	"unicode"
)

// SchemaInfo is the subset of the OSS UC 0.5.1 schema response used by HMS.
// Ownership, properties and storage_root intentionally have no HMS mapping.
type SchemaInfo struct {
	Name            string `json:"name"`
	CatalogName     string `json:"catalog_name"`
	FullName        string `json:"full_name"`
	Comment         string `json:"comment"`
	StorageLocation string `json:"storage_location"`
}

// validSchemaName accepts one exact namespace component, without HMS catalog encoding.
// Case and escaped URI characters are preserved, never normalized.
func validSchemaName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && !strings.ContainsAny(name, ". /\\@#!") && strings.IndexFunc(name, unicode.IsControl) == -1
}

func (c *Client) validateSchema(s SchemaInfo) error {
	if !validSchemaName(s.Name) || s.CatalogName != c.cfg.UCCatalog || (s.FullName != "" && s.FullName != c.cfg.UCCatalog+"."+s.Name) {
		return &Error{Kind: "invalid_response"}
	}
	return nil
}

// ListSchemas aggregates every page for the configured catalog, or returns an error
// without partial metadata. Duplicate identities are treated as inconsistent listing.
func (c *Client) ListSchemas(ctx context.Context) ([]SchemaInfo, error) {
	schemas, err := CollectPages[SchemaInfo](ctx, c, "schemas", url.Values{"catalog_name": {c.cfg.UCCatalog}, "max_results": {"100"}}, "schemas")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		if err := c.validateSchema(s); err != nil {
			return nil, err
		}
		if seen[s.Name] {
			return nil, &Error{Kind: "pagination_duplicate"}
		}
		seen[s.Name] = true
	}
	return schemas, nil
}

// GetSchema retrieves an exact schema in the configured catalog.
func (c *Client) GetSchema(ctx context.Context, name string) (*SchemaInfo, error) {
	if !validSchemaName(name) {
		return nil, &Error{Kind: "invalid_schema_name"}
	}
	var schema SchemaInfo
	if err := c.Get(ctx, "schemas/"+url.PathEscape(c.cfg.UCCatalog+"."+name), nil, &schema); err != nil {
		return nil, err
	}
	if err := c.validateSchema(schema); err != nil {
		return nil, err
	}
	if schema.Name != name {
		return nil, &Error{Kind: "invalid_response"}
	}
	return &schema, nil
}
