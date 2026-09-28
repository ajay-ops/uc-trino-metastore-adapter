# Milestone 2: schema discovery

Implemented scope: `get_all_databases` and `get_database` only. Table RPCs remain
explicit `NOT_IMPLEMENTED` errors. The adapter has no real-HMS client or fallback.

## Behavior

- Schema list uses OSS UC 0.5.1 `GET /schemas` with configured `UC_CATALOG` and `max_results=100`. Every continuation token is followed, including after empty filtered pages. Page/item/byte/deadline limits fail explicitly rather than truncate.
- Exact lookup uses `GET /schemas/{catalog}.{schema}` with URI path escaping. Names are not lowercased. Unsupported namespace components and mismatched catalog/name/full-name responses fail. Duplicate names across pages fail instead of silently deduplicating potentially inconsistent data.
- HMS Database contains name, comment (or empty), exact `storage_location` (or empty), and a non-null empty parameters map. UC owner/properties/storage_root are not exported or used to fabricate grants or locations. HMS owner, privileges and catalog fields remain unset.
- Lookup 404 with `SCHEMA_NOT_FOUND` becomes declared `NoSuchObjectException`. List 404s, missing configured catalogs and generic/proxy 404s are `MetaException` configuration errors. Authentication, authorization, throttling, outages, timeouts and malformed responses remain declared errors. A later-page failure returns no partial names.
- The existing UC client, deadlines, retry and pagination helpers are reused. No repository interfaces, cache, new dependency or generated IDL change was added. RPC metrics distinguish schema success/errors from unsupported table requests.

The pinned API and error behavior were checked against UC tag `v0.5.1`, commit
`cd5df00c650a1fc5c4de3bd8b197549e2bca82fa`: `api/all.yaml`, `SchemaRepository`,
`ErrorCode` and `GlobalExceptionHandler`. The synthetic schema HTTP fixtures follow
that contract; they are not a live UC test.

## Files

New:

- `internal/unity/schemas.go`, `internal/unity/schemas_test.go`
- `internal/translate/schema.go`, `internal/translate/schema_test.go`
- `internal/thrift/schema_test.go`
- `docs/MILESTONE_2.md`

Updated service wiring/handlers/metrics: `cmd/server/main.go`,
`internal/thrift/handler.go`, `internal/thrift/server.go`,
`internal/thrift/server_test.go`. Updated comments/messages:
`internal/translate/doc.go`, `internal/unity/pagination.go`,
`internal/observability/observability.go`, `internal/config/config.go`.
Updated documentation: README, handoff, RPC matrix, implementation plan, UC/HMS
mapping, integration README and IDL provenance README.

## Verification and remaining acceptance

Passed: `go test ./...`, `go test -race ./...`, `go vet ./...`, service build,
formatting check and `make check-generated`. Tests cover complete/empty-intermediate
pagination, opaque tokens, duplicate/malformed/wrong-namespace identities, URI
escaping, exact Database fields, wire empty-list/map encoding, declared
NoSuchObject/MetaException responses, 401/403/404/409/429/503, timeout, partial-page
failure, invalid names, cancellation/error categories and continued table rejection.
Existing pagination limit/cycle, transport/cancellation and unsupported-method
inventory tests also pass.

**The owner explicitly deferred Trino integration test creation/execution.**
Consequently, `SHOW SCHEMAS FROM uc_delta;` against Trino 472 and live UC 0.5.1
remains an unverified acceptance gate. No real HMS participated in any test.
A later test must use an isolated stack with only Trino, this adapter and UC;
seed more than 100 schemas and verify discovery, schema properties and UC outage
behavior. No live SELECT success is claimed.

Before deferral, Docker CLI, Compose and Colima were installed with approval and
the Trino 472 image was downloaded. Both public UC image tags `0.5.1` and `v0.5.1`
were unavailable; a later run needs a verified image or build from the pinned
source. The dedicated `uc-adapter-test` Colima VM was stopped. Installed tooling
and the downloaded image remain available for the deferred work.
