# Milestone 3: table discovery

Implementation scope is complete for the approved table reads. Live SQL acceptance
and comparison to a real HMS response remain pending; neither is claimed below.

## Implemented contract

- `get_table_meta`: `GET /tables` for configured UC catalog and an exact schema, page size 50, complete bounded pagination through empty pages, duplicate/identity validation, and external DELTA filtering. No per-table GET fan-out. The supported table pattern is `*`; arbitrary HMS regex/glob patterns are explicitly rejected. Empty type list or one containing `EXTERNAL_TABLE` includes eligible tables. Other type filters return empty only after successful discovery.
- `get_table_req` and `get_table`: one shared exact UC lookup and translation, identical error behavior. Request capabilities do not grant write access. Any explicit HMS `catName` is rejected because native HMS catalog mapping is disabled; `UC_CATALOG` is the namespace configuration.
- Only UC `EXTERNAL` + `DELTA` records are exposed. Known managed/non-Delta/view records are omitted from lists and explicitly rejected on direct lookup. Unknown enums, malformed identities, conflicting view metadata and invalid eligible-table locations fail rather than disappear.
- Locations use the initial S3 profile (`s3`/`s3a`, bucket authority, no credentials/port/query/fragment/control characters). The exact original location is emitted in both `sd.location` and `sd.serdeInfo.parameters["path"]`; spaces, percent escapes and trailing slashes are not normalized.
- Table parameters contain exactly `spark.sql.sources.provider=DELTA`. StorageDescriptor and SerDe are non-null. HMS columns, partition keys, bucket/sort lists and required maps are explicit empty containers. UC properties/columns are not copied. Trino remains responsible for `_delta_log`, Delta schema, partitions and files.
- Object-specific UC 404s become declared NoSuchObjectException on lookup. Missing configured catalog/generic/proxy 404s remain MetaException. A first-page table-list SCHEMA_NOT_FOUND permits an empty list; later-page failures always abort with no partial result. Pagination now wraps later-page HTTP failures as `pagination_failed`/`UC_LIST_FAILED`, retaining the cause for cancellation/timeout classification. This also makes later-page schema-list failures more explicit.

The approved Trino 472 TCP path needs these three table RPCs. `get_tables` and
`get_tables_by_type` belong to the alternate client path; `get_fields` is not
required for Delta DESCRIBE. They remain UNKNOWN_METHOD, as do all write APIs.
No IDL/generated-code changes, new dependencies, caching, Delta parser, S3 client
or Trino writes were introduced.

## Golden and wire tests

[Fixtures](../integration/golden/README.md) are explicitly **synthetic**, based on
the approved UC/HMS contract. No sanitized real-HMS fixture was available in the
repository. The static expected table is compared against pure translation and
both real-TCP lookup responses. An independent protocol reader checks upstream
field IDs, explicit empty containers and provider/SerDe path on the wire.

The UC fixture contains stale columns and conflicting provider/path/Avro/Delta
properties to verify that they do not override synthesized metadata. Future
real-HMS comparison must use sanitized data for the same UC table and compare
semantics through Trino, not require byte equality with legacy Spark metadata.

## Files

New:

- `internal/unity/tables.go`, `internal/unity/tables_test.go`
- `internal/translate/table.go`, `internal/translate/table_test.go`
- `internal/thrift/table_test.go`
- `integration/table_wire_test.go`
- `integration/golden/README.md`
- `integration/golden/uc_external_delta.json`
- `integration/golden/hms_external_delta.expected.json`
- `docs/MILESTONE_3.md`

Updated: server wiring/startup status; Thrift handler/error mapping/metrics and
existing schema/server tests; pagination error wrapping and tests; translation
package comment; README, handoff, RPC matrix, mapping/implementation-plan status,
integration README and IDL provenance README. Existing Milestone 1/2 reports are
historical records and remain unchanged.

## Verification

Passed:

```text
go test ./...
go test -race ./...
go vet ./...
go build -trimpath -o bin/server ./cmd/server
python3 scripts/generate-thrift.py --check
gofmt -l cmd internal integration   # no output
```

Coverage includes 51 eligible tables over multiple pages with an empty intermediate
page, known ineligible objects, unknown/malformed metadata, preserved/invalid
locations, query/path encoding, first-page versus later-page 404, duplicate/cyclic
pagination, table-type filters, capabilities/catalog restrictions, identical
lookup success/errors, 401/403/429/503, timeouts, malformed JSON, golden responses,
independent wire structure and the complete non-allowlisted RPC rejection inventory.
The first race run found a test-only int/TType comparison compilation error in the
new independent wire reader; it was fixed and the full race suite then passed.

## Remaining acceptance

The earlier owner instruction to defer live Trino integration remains in effect.
No Trino/UC/HMS/S3 integration stack ran for this milestone, and no real HMS was
involved in the local HTTP/TCP contract tests. These SQL results are **unverified**:

```sql
SHOW TABLES FROM uc_delta.raw;
DESCRIBE uc_delta.raw.events;
```

DESCRIBE additionally needs a compatible Delta log and Trino storage credentials;
its columns must be checked against the log, not these empty HMS column lists.
Use Trino 472 and UC 0.5.1 with no real HMS configured/reachable for that gate.
Runtime Java conversion, real-HMS golden comparison and wider storage/identifier
profiles remain explicit follow-up work, not inferred passes from Go fixtures.
