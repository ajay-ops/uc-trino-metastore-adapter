package unity

import (
	"context"
	"encoding/json"
	"net/url"
)

// CollectPages decodes a UC list envelope, following opaque continuation tokens.
// It returns no partial results on failure. T is the endpoint response item type.
func CollectPages[T any](ctx context.Context, c *Client, resource string, query url.Values, collection string) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.OperationTimeout)
	defer cancel()
	if collection == "" || collection == "next_page_token" {
		return nil, &Error{Kind: "invalid_collection"}
	}
	params := make(url.Values, len(query))
	for k, v := range query {
		params[k] = append([]string(nil), v...)
	}
	seen := map[string]bool{}
	if initial := params.Get("page_token"); initial != "" {
		seen[initial] = true
	}
	items := make([]T, 0)
	var bytes int64
	for page := 0; page < c.cfg.MaxPages; page++ {
		var envelope map[string]json.RawMessage
		if err := c.get(ctx, resource, params, &envelope); err != nil {
			if page > 0 {
				return nil, &Error{Kind: "pagination_failed", cause: err}
			}
			return nil, err
		}
		data, ok := envelope[collection]
		if !ok || len(data) == 0 || string(data) == "null" {
			return nil, &Error{Kind: "invalid_response"}
		}
		bytes += int64(len(data) + len(envelope["next_page_token"]))
		if bytes > c.cfg.MaxListBytes {
			return nil, &Error{Kind: "pagination_limit"}
		}
		var batch []T
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, &Error{Kind: "invalid_response"}
		}
		if len(batch) > c.cfg.MaxItems-len(items) {
			return nil, &Error{Kind: "pagination_limit"}
		}
		items = append(items, batch...)
		var next string
		if raw, ok := envelope["next_page_token"]; ok {
			if err := json.Unmarshal(raw, &next); err != nil {
				return nil, &Error{Kind: "invalid_response"}
			}
		}
		if next == "" {
			return items, nil
		}
		if seen[next] {
			return nil, &Error{Kind: "pagination_cycle"}
		}
		seen[next] = true
		params.Set("page_token", next)
	}
	return nil, &Error{Kind: "pagination_limit"}
}
