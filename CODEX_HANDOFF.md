# Codex Handoff — HMS-Free Trino Delta Reading via OSS Unity Catalog

**Project:** `uc-trino-metastore-adapter`  
**Status:** Plan approved; Milestone 3 table discovery implemented; live Trino acceptance pending (see docs/MILESTONE_3.md)  
**Primary goal:** Production-grade Trino read path for native Delta tables using OSS Unity Catalog, with **no Hive Metastore service or HMS backing database**  
**Recommended implementation language:** Go  
**POC baseline date:** September 2026

## Implementation update — 2026-09-28

Milestone 3 implements the five approved read RPCs against UC: schema discovery,
external-Delta table listing and shared request/legacy table lookup. Synthetic HMS
metadata carries provider and unchanged storage/SerDe path; `_delta_log` remains
authoritative for schema. No optional RPCs or writes were added. See
[Milestone 3](docs/MILESTONE_3.md) for tests and the pending live SQL/real-HMS
comparison gates under the earlier integration deferral.

## Source verification addendum — 2026-09-25

The source-reviewed contract is now in [IMPLEMENTATION_PLAN](docs/IMPLEMENTATION_PLAN.md),
[TRINO_CALL_FLOW](docs/TRINO_CALL_FLOW.md), [RPC_MATRIX](docs/RPC_MATRIX.md),
[UC_HMS_MAPPING](docs/UC_HMS_MAPPING.md), [TEST_PLAN](docs/TEST_PLAN.md), and
[RISKS_AND_OPEN_QUESTIONS](docs/RISKS_AND_OPEN_QUESTIONS.md). These documents supersede
the provisional RPC classifications and assumptions below. The original review preceded implementation approval; current implementation status
is recorded above. Live Trino interoperability remains unverified.

Corrections established from pinned sources:

- The normal Trino 472 TCP discovery/read path needs three successful RPCs:
  `get_all_databases`, `get_table_meta`, and `get_table_req`. The proposed first
  implementation also includes `get_table` and `get_database` for compatibility.
  Schema existence uses schema enumeration; `get_database` serves schema properties.
- The 472 alternative table-listing branch is selected by client construction
  (HTTP versus TCP), not automatic fallback after a `get_table_meta` exception.
  Trino 483 removes that branch. Table lookup still has an alternative-call path.
- The exact 472 wire IDL is `trinodb/hive-thrift` tag 2, not necessarily the full
  Hive 3.1.3 server IDL. Version 483 uses tag 3; the inspected IDL difference is a
  timestamp-statistics extension, not a change to the proposed five read RPCs.
- Provider/path alone are not structurally sufficient: Trino also requires valid
  names/type, storage descriptor, SerDe info and non-null column/partition lists.
  The initial contract uses empty lists, leaving actual schema/partitions in Delta.
- `get_fields`, HMS statistics and partition APIs are not required for these native
  Delta paths. View probes reuse table GET/list RPCs.
- Stock Trino can suppress per-table column-discovery errors. Correct adapter
  errors cannot guarantee atomic `information_schema.columns` results.
- UC v0.5.1 authenticates UC INTERNAL-issuer tokens when security is enabled;
  arbitrary external IdP JWTs are not directly interchangeable with those tokens.
- Trino 483 is the current production candidate inspected; 472 remains the POC
  target. Neither release is runtime-certified by this planning phase.

Initial scope proposed for approval: external Delta tables only, one UC catalog
per adapter deployment, service identity, no impersonation, no adapter cache,
explicitly disabled metadata write-back and enforced Trino/Privacera read-only
policy. Rejecting HMS mutation RPCs alone cannot prevent direct S3 writes.

---

## 0. How Codex Should Use This File

This file is the canonical handoff for planning and implementing the project.

Before changing code, Codex must:

1. Read this file completely.
2. Verify source-sensitive claims against the exact Trino and Unity Catalog versions being targeted.
3. Update this document whenever an architectural assumption changes.
4. Never leave important architecture decisions only in chat history.
5. Treat anything marked **VERIFY** as unresolved until confirmed in source/tests.
6. Prefer source code and official documentation over assumptions.
7. Do not expand scope beyond what is necessary for production-grade **Trino read-only Delta access**.

The first deliverable is **not code**. The first deliverable is a **source-verified implementation plan and RPC contract matrix**.

---

# 1. Project Objective

Replace the Hive Metastore dependency in the Trino Delta read path while preserving:

- OSS Unity Catalog as the authoritative catalog.
- Native Delta Lake as the table format.
- AWS S3 as the data store.
- Spark as the initial Delta writer.
- Trino as the initial analytical reader.
- The official Trino Delta Lake connector.
- Privacera as the existing governance/enforcement layer initially.

The target production architecture must not require:

- Hive Metastore service.
- HMS backing database.
- Iceberg.
- Delta UniForm.
- A custom Delta transaction-log reader.
- A custom Parquet reader.
- A fork of Trino unless source analysis proves it unavoidable.

---

# 2. Hard Requirements

## Mandatory

- **HMS service = 0**
- **HMS backing DB = 0**
- OSS Unity Catalog is the authoritative metadata catalog.
- Delta Lake remains the native and authoritative table format.
- S3 remains the data layer.
- Spark can write Delta.
- Trino can read Delta.
- Existing external Delta table locations should remain unchanged.
- Initial Trino integration is read-only.
- Official Trino Delta reader must be reused.
- Production implementation must include HA, metrics, tracing, retries, timeouts, error mapping, security, tests, rollout, and rollback.

## Explicitly out of scope for the first production version

- Trino `INSERT`
- Trino `UPDATE`
- Trino `DELETE`
- Trino `MERGE`
- Trino `CREATE TABLE`
- Trino `ALTER TABLE`
- Trino `DROP TABLE`
- UC credential vending to Trino
- Replacing Privacera
- Iceberg compatibility
- Delta UniForm
- Reimplementing Delta transaction-log parsing

---

# 3. Current / Tested Stack

| Component | POC / Current Baseline |
|---|---|
| Spark | 3.5.9 / Scala 2.12 |
| Delta Lake | 3.3.2 |
| OSS Unity Catalog | 0.5.1 |
| UC Spark connector | `unitycatalog-spark_2.12-0.2.1.jar` |
| UC Java client | `unitycatalog-client-0.2.1.jar` |
| Trino | 472 |
| Existing HMS | 3.1.3 |
| Storage | AWS S3 |
| Table format | Native Delta |
| Governance | Privacera |
| Trino role initially | Reader |
| Spark role initially | Writer |

Production certification should also evaluate a newer/current Trino release before final rollout.

---

# 4. POC Verdict That Led to This Architecture

Two blockers were identified in the stock OSS paths.

## B1 — Trino native Delta reading exists, but OSS UC is not a supported Delta metastore backend

The official Trino Delta connector natively reads Delta transaction logs, but the normal metadata path is backed by Hive Metastore / compatible metastore integration.

The missing integration is:

```text
Official Trino Delta connector
        |
        X
        |
OSS Unity Catalog
```

The problem is **not** Delta reading. The problem is **table discovery / metadata lookup**.

## B2 — Delta UniForm is not an HMS-free workaround in the tested OSS path

The tested Delta 3.3.2 UniForm flow traversed Spark SessionCatalog / HiveCatalog and required HMS during Iceberg metadata generation.

Therefore this target was rejected:

```text
Spark -> Delta -> UniForm -> Iceberg metadata -> UC Iceberg REST -> Trino
```

because the producer side still required HMS.

## Result

Rejected:

```text
Option A: Trino Delta + HMS
Option B: Delta UniForm + Iceberg REST
```

Selected for further engineering:

```text
Option C:
Unity Catalog metadata + official Trino Delta reader + native Delta on S3
```

---

# 5. Selected Production Architecture

```text
                        OSS UNITY CATALOG
                               ^
                               |
                          REST / JWT
                               |
                  +-------------------------+
                  | UC Metadata Adapter     |
                  |                         |
                  | Go service              |
                  | HMS Thrift-compatible   |
                  | API on :9083            |
                  +------------^------------+
                               |
                        HMS Thrift protocol
                               |
                               v
                    Official Trino Delta
                       Lake connector
                               |
                               v
                    TransactionLogAccess
                               |
                               v
                       Native Delta
                               |
                  +------------+------------+
                  |                         |
              _delta_log                 Parquet
                  |                         |
                  +------------+------------+
                               |
                               v
                              S3


Spark
  |
  +---- UC client -----------> OSS Unity Catalog
  |
  +---- Delta writer --------> S3
```

## Important terminology

This design removes **Hive Metastore infrastructure**, but preserves **HMS wire/API compatibility** between Trino and the adapter.

There is:

```text
NO Hive Metastore server
NO HMS backing database
NO HMS metadata ownership
NO Iceberg
NO UniForm
```

There is:

```text
HMS-compatible Thrift API between Trino and the adapter
```

The adapter is not a metastore database. It is a stateless protocol translation service.

Unity Catalog remains authoritative.

---

# 6. Why We Prefer a Thrift-Compatible Adapter Instead of Forking Trino

The official Delta connector already separates catalog lookup from Delta processing.

The key Trino abstraction is:

```java
DeltaLakeMetastore
```

Verified in Trino 472:

```text
plugin/trino-delta-lake/src/main/java/
io/trino/plugin/deltalake/metastore/DeltaLakeMetastore.java
```

Relevant methods:

```java
List<String> getAllDatabases();
Optional<Database> getDatabase(String databaseName);
List<TableInfo> getAllTables(String databaseName);
Optional<Table> getRawMetastoreTable(String databaseName, String tableName);
Optional<DeltaMetastoreTable> getTable(String databaseName, String tableName);
```

Mutation methods also exist, but the initial implementation will reject write/mutation behavior.

Current implementation:

```text
DeltaLakeMetastore
       |
       v
HiveMetastoreBackedDeltaLakeMetastore
       |
       v
HiveMetastore
       |
       v
Thrift Hive Metastore client
       |
       v
HMS
```

Target:

```text
DeltaLakeMetastore
       |
       v
HiveMetastoreBackedDeltaLakeMetastore
       |
       v
HiveMetastore
       |
       v
Thrift client
       |
       v
UC Metadata Adapter
       |
       v
Unity Catalog REST API
```

This keeps Trino unmodified.

---

# 7. Verified Trino 472 Behavior

## 7.1 DeltaLakeMetastore interface

Source:

https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/DeltaLakeMetastore.java

Verified methods:

```text
getAllDatabases
getDatabase
getAllTables
getRawMetastoreTable
getTable

createDatabase
dropDatabase
createTable
replaceTable
dropTable
renameTable
```

## 7.2 HiveMetastoreBackedDeltaLakeMetastore is a thin wrapper

Source:

https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/HiveMetastoreBackedDeltaLakeMetastore.java

Verified behavior:

```text
getAllDatabases()     -> delegate.getAllDatabases()
getDatabase()         -> delegate.getDatabase()
getAllTables()        -> delegate.getTables()
getRawMetastoreTable()-> delegate.getTable()
```

A Delta table is verified using:

```text
spark.sql.sources.provider = DELTA
```

The table location is retrieved from:

```text
table.getStorage().getSerdeParameters()["path"]
```

This field is critical.

## 7.3 After table discovery, Trino reads Delta directly

Source:

https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java

Important path:

```text
DeltaLakeMetadata.getTableHandle()
        |
        v
metastore.getRawMetastoreTable()
        |
        v
convert to Delta metastore table
        |
        v
table location
        |
        v
TransactionLogAccess
        |
        v
_delta_log
```

Trino subsequently obtains Delta metadata and protocol from `_delta_log`.

Therefore the adapter must **not** reimplement Delta internals.

---

# 8. Core Query Read Flow

Example:

```sql
SELECT *
FROM uc_delta.raw_bdp.account_info;
```

Expected flow:

```text
User
 |
 v
Trino coordinator
 |
 v
DeltaLakeMetadata.getTableHandle()
 |
 v
DeltaLakeMetastore.getRawMetastoreTable()
 |
 v
HiveMetastoreBackedDeltaLakeMetastore
 |
 v
HiveMetastore.getTable()
 |
 v
ThriftHiveMetastoreClient
 |
 v
get_table_req
 |
 v
uc-trino-metastore-adapter
 |
 | GET UC table
 v
OSS Unity Catalog
 |
 | returns DELTA + storage location
 v
adapter synthesizes HMS Table
 |
 v
Trino validates:
spark.sql.sources.provider=DELTA
 |
 v
Trino extracts:
serdeParameters["path"]
 |
 v
TransactionLogAccess
 |
 v
s3://.../_delta_log
 |
 +--> Delta metadata
 +--> protocol
 +--> active files
 |
 v
Trino workers
 |
 v
Parquet on S3
```

Old HMS must not appear anywhere in this execution path.

---

# 9. Source-Verified HMS / Thrift Client Behavior

Relevant Trino 472 source:

https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java

Verified important behaviors:

## Table lookup

Trino can prefer:

```text
getTableReq(...)
```

and fall back to:

```text
getTable(...)
```

The adapter should implement both.

## Table listing

Trino uses:

```text
getTableMeta(...)
```

and contains compatibility fallback behavior involving:

```text
getTables(...)
getTablesByType(...)
```

The adapter should implement all three for production compatibility.

## Other client methods present

Source inspection shows client support for:

```text
getAllDatabases
getDatabase
getFields
getTableNamesByFilter
getTableStatisticsReq
setUGI
```

Not all are necessarily required for the first read-only Delta path. Actual workload tracing must determine which are mandatory.

---

# 10. Required RPC Contract — Initial Classification

This classification is a starting point. Codex must verify it against exact source, real traffic, and tests.

| HMS / Thrift function | Initial classification | Purpose |
|---|---|---|
| `get_all_databases` | REQUIRED | `SHOW SCHEMAS`, metadata discovery |
| `get_databases` | COMPATIBILITY | Pattern/catalog compatibility |
| `get_database` | REQUIRED | Schema resolution |
| `get_table_req` | REQUIRED | Preferred table lookup |
| `get_table` | REQUIRED | Fallback table lookup |
| `get_table_meta` | REQUIRED | Table listing |
| `get_tables` | COMPATIBILITY / likely required | Listing fallback |
| `get_tables_by_type` | COMPATIBILITY / likely required | Listing/view fallback |
| `get_fields` | VERIFY | Some metadata/discovery paths |
| `get_table_names_by_filter` | VERIFY | Parameter-based filtering paths |
| `get_table_statistics_req` | VERIFY | May not be needed for Delta read path |
| `set_ugi` | VERIFY | Identity / impersonation behavior |
| role/privilege RPCs | NOT INITIAL SCOPE | Privacera remains authorization layer |
| table mutation RPCs | MUST REJECT | Read-only Trino |
| DB mutation RPCs | MUST REJECT | Read-only Trino |
| partition mutation RPCs | MUST REJECT | Read-only Trino |
| stats mutation RPCs | MUST REJECT | Read-only Trino |

Do **not** implement the full HMS API unless production behavior requires it.

---

# 11. SQL-to-RPC Matrix That Must Be Completed Before Coding

Codex must produce a source-verified matrix for at least:

```text
SHOW SCHEMAS
SHOW TABLES
DESCRIBE
SHOW CREATE TABLE
information_schema.schemata
information_schema.tables
information_schema.columns
SELECT *
SELECT count(*)
SELECT with partition predicate
Delta time travel read, if supported
Superset schema discovery
Superset table discovery
Superset column discovery
```

Required output:

| SQL / Operation | Trino method | DeltaLakeMetastore | HiveMetastore | Thrift RPC | UC API | Required response fields |
|---|---|---|---|---|---|---|

This matrix becomes the implementation contract.

---

# 12. Unity Catalog 0.5.1 API Surface

Source:

https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/api/all.yaml

Verified relevant APIs:

## Schemas

```text
GET /schemas?catalog_name=<catalog>
GET /schemas/{full_name}
```

List APIs are paginated and can return `next_page_token`.

## Tables

```text
GET /tables?catalog_name=<catalog>&schema_name=<schema>
GET /tables/{full_name}
```

List APIs are paginated.

The OpenAPI model contains table metadata including fields such as:

```text
catalog_name
schema_name
name
table_type
data_source_format
storage_location
columns
properties
```

Codex must verify exact `TableInfo` and `SchemaInfo` definitions in tag `v0.5.1` before implementation.

---

# 13. Namespace Mapping

Recommended rule:

```text
one Trino catalog = one UC catalog
```

Example:

```text
Trino:
uc_delta.raw_bdp.account_info
```

maps to:

```text
UC:
unity.raw_bdp.account_info
```

Configuration:

```text
TRINO_CATALOG=uc_delta
UC_CATALOG=unity
```

For multiple UC catalogs, initially prefer multiple Trino catalogs rather than complex namespace flattening.

---

# 14. UC Table -> HMS Table Translation

Example UC response conceptually:

```json
{
  "catalog_name": "unity",
  "schema_name": "raw_bdp",
  "name": "account_info",
  "table_type": "EXTERNAL",
  "data_source_format": "DELTA",
  "storage_location": "s3://bucket/raw_bdp/account_info"
}
```

The adapter needs to synthesize an HMS-compatible `Table`.

Minimum concept:

```text
dbName    = raw_bdp
tableName = account_info

tableType = EXTERNAL_TABLE

parameters:
  spark.sql.sources.provider = DELTA

storage / serde:
  path = s3://bucket/raw_bdp/account_info
```

The exact generated Thrift object must be verified against:

- Trino 472 `HiveMetastoreBackedDeltaLakeMetastore`
- Hive metastore Thrift models
- real HMS responses for known Delta tables

## Golden rule

Must be present:

```text
spark.sql.sources.provider = DELTA
```

Must be present where Trino reads it:

```text
table.storage.serdeParameters["path"]
```

The actual Delta schema remains authoritative in `_delta_log`.

---

# 15. Schema Authority

Do not create a second schema authority.

Desired ownership:

```text
UC:
table identity
namespace
format
storage location
catalog-level metadata

Delta _delta_log:
actual Delta schema
partition columns
protocol
features
active files
transaction state
```

The adapter should not try to synchronize a second Delta schema representation unless the HMS response structurally requires fields.

**VERIFY:** whether to return UC columns or a dummy/minimal Delta column representation for each relevant RPC.

---

# 16. Recommended Go Service Structure

```text
uc-trino-metastore-adapter/
│
├── AGENTS.md
├── README.md
├── CODEX_HANDOFF.md
│
├── cmd/
│   └── server/
│       └── main.go
│
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── thrift/
│   │   ├── server.go
│   │   ├── handler.go
│   │   └── generated/
│   ├── unity/
│   │   ├── client.go
│   │   ├── auth.go
│   │   ├── schema.go
│   │   ├── table.go
│   │   ├── pagination.go
│   │   └── errors.go
│   ├── translate/
│   │   ├── database.go
│   │   ├── table.go
│   │   ├── tablemeta.go
│   │   ├── fields.go
│   │   └── errors.go
│   ├── cache/
│   │   └── cache.go
│   └── observability/
│       ├── metrics.go
│       ├── logging.go
│       └── tracing.go
│
├── integration/
│   ├── trino/
│   ├── unity/
│   ├── spark/
│   └── golden/
│
├── deploy/
│   ├── kubernetes/
│   └── helm/
│
└── docs/
    ├── RPC_MATRIX.md
    ├── UC_HMS_MAPPING.md
    ├── TEST_PLAN.md
    └── RUNBOOK.md
```

Keep the adapter stateless. Do not add a database.

---

# 17. Why Go

Go is preferred because this service will sit on the Trino metadata critical path.

Advantages:

- Static binary.
- Strong concurrency model.
- Strong typing for generated Thrift structures.
- Low operational footprint.
- Good Kubernetes ergonomics.
- Strong HTTP, Prometheus, and OpenTelemetry ecosystem.

Python is acceptable only for a throwaway protocol POC if necessary.

---

# 18. Error Translation Rules

Never turn all UC failures into "table not found".

Initial mapping:

| UC / transport condition | Adapter behavior |
|---|---|
| UC 404 | `NoSuchObjectException` / appropriate HMS not-found response |
| UC 401 | authentication failure |
| UC 403 | authorization failure |
| UC 409 | explicit conflict / metadata error |
| UC 429 | retryable upstream throttling error |
| UC 5xx | `MetaException` / metastore service failure |
| connection timeout | retryable metastore service failure |
| malformed JSON / invalid UC response | `MetaException`, no fake 404 |
| pagination failure | fail listing operation; never silently return partial data |

Codex must map these to the exact exceptions Trino handles correctly.

---

# 19. Retries, Timeouts, and Circuit Breaking

Requirements:

- Explicit connect timeout.
- Explicit request timeout.
- Bounded exponential retry with jitter.
- Retry only safe/idempotent reads.
- Respect 429 retry hints if provided.
- Never retry 401/403 blindly.
- Avoid retry storms.
- Export retry and upstream-latency metrics.
- Return service failures rather than fake empty metadata.

Exact values must be load-tested.

---

# 20. Pagination Is a First-Class Requirement

UC list endpoints are paginated.

Therefore:

```text
SHOW TABLES
SHOW SCHEMAS
information_schema queries
```

must consume all pages.

Never return only page 1.

Tests must force multiple pages.

---

# 21. Caching Rules

Allowed to cache:

```text
schema existence
schema list
table list
UC table -> storage location
```

Never cache:

```text
Delta table version
Delta transaction snapshot
Delta schema state
Delta active file list
Delta protocol state
```

Caching requirements:

- Small TTL.
- Bounded memory.
- Hit/miss/eviction metrics.
- No persistence.
- Rename/drop behavior tested.

Start conservatively and optimize from measurements.

---

# 22. Authentication / Security

Initial production model:

```text
Trino
  |
  | HMS-compatible Thrift
  v
Adapter
  |
  | service identity / JWT / OIDC
  v
Unity Catalog
```

S3 access initially remains:

```text
Trino workers
  |
  | existing IAM identity
  v
S3
```

Privacera remains the existing governance/enforcement layer.

Do not combine the first adapter release with UC credential vending.

## Important limitation

Per-user identity does not automatically propagate from Trino through an HMS compatibility bridge into UC authorization.

`setUGI` behavior should be studied, but it must not be assumed to provide secure end-user UC identity propagation.

Initial design:

```text
UC authentication = adapter service identity
authorization      = existing Trino / Privacera controls
S3 authorization   = existing Trino IAM model
```

---

# 23. Read-Only Enforcement

Read-only must be enforced at multiple layers.

## Adapter

Reject mutation RPCs.

## Trino

Configure access control so the UC-backed Delta catalog cannot perform:

```text
CREATE
DROP
ALTER
INSERT
UPDATE
DELETE
MERGE
```

## Delta connector

Keep metastore metadata-writing features disabled unless explicitly proven safe.

Relevant config property:

```text
delta.metastore.store-table-metadata
```

Codex must verify default and behavior in the exact target Trino release.

---

# 24. Observability

Expose at minimum:

## Adapter request metrics

```text
adapter_requests_total{rpc,status}
adapter_request_duration_seconds{rpc}
adapter_inflight_requests
```

## UC upstream metrics

```text
uc_requests_total{endpoint,status}
uc_request_duration_seconds{endpoint}
uc_retries_total{endpoint}
uc_throttles_total
uc_timeouts_total
```

## Cache metrics

```text
cache_hits_total{type}
cache_misses_total{type}
cache_evictions_total{type}
cache_entries{type}
```

## Translation metrics

```text
translation_failures_total{type}
unsupported_rpc_total{rpc}
invalid_uc_table_total{reason}
```

## Health

- Liveness: process/server health.
- Readiness: initialized and able to serve safely.
- Dependency health should be exposed separately.

## Tracing

Trace:

```text
Trino RPC -> adapter -> UC REST
```

Never log secrets, JWTs, or AWS credentials.

---

# 25. HA / Kubernetes Design

Adapter is stateless.

```text
                 Kubernetes Service
                        |
            +-----------+-----------+
            |           |           |
          pod-1       pod-2       pod-3
```

Requirements:

- At least 2 replicas in production.
- Pod disruption budget.
- Graceful shutdown.
- Rolling deployment.
- Resource requests/limits based on load tests.
- No local authoritative state.
- No adapter database.

Unity Catalog remains the source of truth.

---

# 26. Performance Design

Measure:

```text
p50/p95/p99 RPC latency
UC upstream latency
Trino planning latency
QPS
concurrency
memory
CPU
GC
```

Test:

- high-concurrency query planning
- Superset metadata storms
- large `SHOW TABLES`
- many schemas
- many tables
- UC throttling
- repeated table lookup

Do not optimize before baseline measurements exist.

---

# 27. Production Test Matrix

## Functional

- `SHOW SCHEMAS`
- `SHOW TABLES`
- `DESCRIBE`
- `SHOW CREATE TABLE`
- `information_schema.schemata`
- `information_schema.tables`
- `information_schema.columns`
- `SELECT *`
- `SELECT count(*)`
- projection
- partition predicates
- joins
- aggregations
- Superset discovery

## Delta behavior

- Spark creates UC external Delta table.
- Spark writes version N.
- Trino reads version N.
- Spark writes version N+1.
- Trino reads N+1 without adapter metadata mutation.
- schema evolution
- add column
- partitioned tables
- Delta checkpoints
- large `_delta_log`
- column mapping, if production uses it
- supported Delta protocol/features
- time travel, if required

## Catalog lifecycle

- table create by Spark
- table rename
- table drop
- schema create/drop
- storage location change: define whether allowed
- non-Delta UC table must not be exposed as a valid Delta table

## Pagination

- schemas > one UC page
- tables > one UC page

## Failures

- UC unavailable
- UC timeout
- UC 401
- UC 403
- UC 404
- UC 429
- UC 500
- malformed UC response
- adapter pod restart
- rolling restart
- S3 unavailable
- S3 403
- corrupt Delta log
- deleted table during query planning

## Load

- 100+ concurrent query-planning operations
- Superset dashboard fan-out
- repeated metadata discovery
- large namespace

## Critical retirement test

Physically stop or network-block old HMS.

Verify:

```text
old HMS connections = 0
```

Run the complete read-only test suite.

---

# 28. Golden Contract Testing

Before HMS retirement, use the current HMS as a compatibility oracle.

For the same known Delta table:

```text
Trino -> existing HMS
Trino -> UC adapter
```

Capture/compare relevant HMS responses:

- Database
- Table
- TableMeta
- table type
- provider property
- storage descriptor
- serde parameters
- location
- fields where relevant

The adapter output should be semantically compatible with what Trino expects, not necessarily byte-for-byte identical.

Store safe sanitized fixtures in:

```text
integration/golden/
```

---

# 29. Real Traffic / RPC Capture Before Final Implementation

Do not rely only on source reading.

Before finalizing the required RPC surface:

1. Observe Trino 472 -> HMS calls in a POC environment.
2. Run normal SELECTs, metadata queries, Superset discovery, and dashboards.
3. Capture method name, rate, latency, success/failure.
4. Build a frequency table.
5. Compare with source-derived RPC matrix.

Goal:

```text
Implement the exact production read contract,
not the entire HMS API.
```

---

# 30. Production Rollout Plan

## Phase 0 — Source verification

- Complete RPC matrix.
- Complete UC -> HMS translation spec.
- Verify target Trino release.
- Verify UC 0.5.1 APIs.
- Verify HMS Thrift IDL/version compatibility.

## Phase 1 — Minimal POC adapter

Implement minimum required read methods, initially expected to include:

```text
get_all_databases
get_database
get_table_req
get_table
get_table_meta
```

Target:

```text
SHOW SCHEMAS
SHOW TABLES
DESCRIBE
SELECT
```

## Phase 2 — Compatibility surface

Add only methods proven necessary, e.g.:

```text
get_tables
get_tables_by_type
get_fields
get_databases
get_table_names_by_filter
```

## Phase 3 — Local integration

Run Trino 472 against adapter with old HMS unavailable.

## Phase 4 — EKS POC

Deploy adapter + UC + Trino test catalog using existing S3 Delta data.

## Phase 5 — Parallel catalog validation

Example:

```text
delta_hms
uc_delta
```

Run equivalent queries and compare results.

## Phase 6 — Superset canary

Point selected workloads to `uc_delta` and observe.

## Phase 7 — Production read-only traffic

Gradually move read workloads.

## Phase 8 — HMS isolation test

Block Trino from old HMS and observe for a defined stability window.

## Phase 9 — HMS retirement

Only after zero dependency is confirmed and rollback/runbooks are complete.

---

# 31. Rollback Strategy

During migration retain:

```text
delta_hms -> old path
uc_delta  -> new adapter path
```

Rollback is a Trino catalog / datasource routing change, not data movement.

Do not rewrite Delta data for this migration.

---

# 32. Upgrade Strategy

POC baseline is Trino 472.

Before production:

1. Compare `DeltaLakeMetastore`, `HiveMetastoreBackedDeltaLakeMetastore`, `DeltaLakeMetadata`, and Thrift client behavior between 472 and the intended production Trino version.
2. Run the adapter integration suite against both.
3. Treat Trino compatibility as a first-class test dimension.

Maintain a compatibility matrix:

| Adapter version | Trino version | UC version | Certified |
|---|---|---|---|
| TBD | 472 | 0.5.1 | POC |
| TBD | production target | production target | Required |

---

# 33. Sources / References

## Trino 472

DeltaLakeMetastore:  
https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/DeltaLakeMetastore.java

HiveMetastoreBackedDeltaLakeMetastore:  
https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/HiveMetastoreBackedDeltaLakeMetastore.java

DeltaLakeMetadata:  
https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java

ThriftHiveMetastoreClient:  
https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java

ThriftHiveMetastore:  
https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastore.java

HiveMetastore interface:  
https://github.com/trinodb/trino/blob/472/lib/trino-metastore/src/main/java/io/trino/metastore/HiveMetastore.java

## Unity Catalog 0.5.1

OpenAPI:  
https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/api/all.yaml

---

# 34. Important Things Codex Must NOT Assume

Do not assume:

- every HMS RPC must be implemented
- UC columns can blindly be converted to HMS columns
- `Storage.location` alone is enough; Trino 472 reads serde `path`
- HMS authentication semantics equal UC authentication semantics
- `setUGI` automatically provides safe UC end-user identity
- Trino writes are safe because reads work
- a UC outage should look like "table missing"
- only one page of UC results exists
- POC Trino version is automatically the production version
- Delta 3.3.2 behavior defines all future Delta versions
- Trino catalog metadata should own Delta schema
- adapter should parse `_delta_log`
- Iceberg/UniForm should be reintroduced unless requirements change

---

# 35. Open Questions Codex Must Resolve

1. Exact minimal RPC list for Trino read path.
2. Exact minimal RPC list for Superset discovery and `information_schema`.
3. Exact HMS Thrift IDL/version used by Trino 472.
4. Best Go Thrift generation strategy compatible with Trino.
5. Exact fields required in `Database`, `Table`, `StorageDescriptor`, `SerDeInfo`, `TableMeta`, and `FieldSchema`.
6. Whether UC columns or minimal/dummy columns should be returned for each call.
7. Exact exceptions that produce correct Trino behavior.
8. Whether `get_table_statistics_req` is used in targeted Delta read flows.
9. Whether `get_table_names_by_filter` is required in real workloads.
10. Superset-specific metadata RPC patterns.
11. Correct behavior for views and non-Delta UC tables.
12. Correct behavior for managed UC Delta tables versus external tables.
13. Whether phase 1 should expose only external Delta tables.
14. Rename/drop cache invalidation behavior.
15. Trino metastore client concurrency and connection behavior.
16. HMS-side TLS/mTLS requirements.
17. Production target Trino version.
18. Production target UC version.
19. Any UC 0.5.1 experimental API limitations affecting this design.
20. Any hidden dependency in the official Delta connector that still assumes a real HMS.

---

# 36. Definition of Done — POC

POC succeeds only if:

```text
HMS service stopped
HMS network path blocked
HMS database unused

Spark creates/writes native Delta through UC

Trino:
  SHOW SCHEMAS works
  SHOW TABLES works
  DESCRIBE works
  SELECT works
  partition-filtered SELECT works

Spark commit N+1 is visible to a subsequent Trino query

No Iceberg metadata
No UniForm
No Trino fork
```

---

# 37. Definition of Done — Production

Production-ready means:

- all required metadata SQL works
- Superset works
- pagination works completely
- error translation validated
- retries/timeouts tested
- HA tested
- rolling upgrade tested
- UC outage tested
- S3 outage tested
- adapter outage tested
- target Trino version certified
- load tested
- metrics/dashboards/alerts complete
- runbook complete
- rollback proven
- security review complete
- Privacera behavior explicitly validated
- old HMS traffic proven zero
- old HMS decommissioned after observation window

---

# 38. Repository Documentation Discipline

After repository creation, keep:

```text
AGENTS.md
CODEX_HANDOFF.md
docs/RPC_MATRIX.md
docs/UC_HMS_MAPPING.md
docs/TEST_PLAN.md
docs/RUNBOOK.md
```

## Suggested AGENTS.md

```markdown
# Project Rules

- Unity Catalog is authoritative.
- No Hive Metastore service or DB in target architecture.
- No Iceberg or UniForm.
- Keep the official Trino Delta reader.
- Prefer no Trino fork.
- Initial Trino path is read-only.
- Adapter is stateless.
- Do not parse Delta logs in the adapter.
- Verify source before implementing an RPC.
- Add contract tests for every supported RPC.
- Update architecture/docs when assumptions change.
```

Do not duplicate this entire handoff into `AGENTS.md`.

---

# 39. First Codex Planning Prompt

Use this after adding `CODEX_HANDOFF.md` and `AGENTS.md` to the repository.

```text
Read AGENTS.md and CODEX_HANDOFF.md completely.

Do not write implementation code yet.

You are the principal engineer responsible for producing a source-verified
production implementation plan for this architecture:

Official Trino Delta connector
        |
        v
HMS-Thrift-compatible Go adapter
        |
        v
OSS Unity Catalog
        |
        v
table storage location
        |
        v
Trino native Delta reader
        |
        v
_delta_log + Parquet on S3

Hard constraints:

- no Hive Metastore service
- no HMS database
- no Iceberg
- no UniForm
- Unity Catalog is authoritative
- Spark is the initial Delta writer
- Trino is initially read-only
- do not fork Trino unless source analysis proves it is necessary
- do not implement Delta or Parquet processing in the adapter

First perform source analysis.

For Trino 472 and the intended current production Trino release, inspect:

- DeltaLakeMetastore.java
- HiveMetastoreBackedDeltaLakeMetastore.java
- DeltaLakeMetadata.java
- HiveMetastore.java
- ThriftHiveMetastore.java
- ThriftHiveMetastoreClient.java
- all directly relevant metastore client/server classes

For OSS Unity Catalog 0.5.1 inspect:

- api/all.yaml
- SchemaInfo
- TableInfo
- list/get schemas
- list/get tables
- pagination
- auth/error behavior

Deliver, before code:

1. Exact SQL -> Trino -> HiveMetastore -> Thrift RPC call graph for:
   SHOW SCHEMAS
   SHOW TABLES
   DESCRIBE
   SHOW CREATE TABLE
   information_schema queries
   SELECT
   partition-filtered SELECT
   Superset metadata discovery.

2. A complete RPC matrix classifying every relevant method as:
   REQUIRED
   COMPATIBILITY
   OPTIONAL
   MUST-REJECT-WRITES.

3. Exact UC -> HMS model mapping including required fields.

4. Identify every field Trino actually consumes from the synthesized
   HMS Table object.

5. Verify whether serdeParameters["path"] and
   spark.sql.sources.provider=DELTA are sufficient for the core table
   discovery path.

6. Determine the correct column representation for HMS responses while
   keeping _delta_log authoritative.

7. Exact error mapping from UC/HTTP/network failures into HMS Thrift
   exceptions expected by Trino.

8. Go Thrift implementation strategy and generated-code plan.

9. Package/module design.

10. Security model.

11. HA, caching, retry, timeout, observability design.

12. Test plan and golden-contract strategy.

13. Differences between Trino 472 and the intended current Trino version.

14. All hidden HMS dependencies or architecture blockers.

15. Update:
    docs/RPC_MATRIX.md
    docs/UC_HMS_MAPPING.md
    docs/TEST_PLAN.md

Do not implement production code until this plan is reviewed.
For every important conclusion cite the exact source file/class/function
and repository URL.
```

---

# 40. Implementation Milestones After Plan Approval

## Milestone 1 — Protocol skeleton

- Go service boots.
- Thrift server listens.
- health/metrics endpoints exist.
- unsupported methods explicitly fail.
- UC client authenticates.

## Milestone 2 — Schema discovery

Implement verified schema RPCs.

Test:

```text
SHOW SCHEMAS
```

## Milestone 3 — Table discovery

Implement verified table-list/table-get methods.

Test:

```text
SHOW TABLES
DESCRIBE
```

## Milestone 4 — Native Delta SELECT

Return enough metadata for official Delta connector to obtain S3 path.

Test:

```text
SELECT *
SELECT count(*)
```

Old HMS must be unavailable.

## Milestone 5 — Compatibility / Superset

Add only RPCs proven necessary.

Test:

```text
information_schema
Superset datasource discovery
dashboard queries
```

## Milestone 6 — Production hardening

- retries
- timeouts
- pagination
- cache
- HA
- metrics
- tracing
- security
- failure injection
- load testing

## Milestone 7 — Parallel validation / canary

```text
delta_hms
vs
uc_delta
```

## Milestone 8 — HMS retirement

Block old HMS, observe, then decommission.

---

# 41. Review Gates

## Gate A — Architecture

- Does any read path still require real HMS?
- Does adapter remain stateless?
- Does Trino still use official Delta processing?
- Is Unity Catalog authoritative?

## Gate B — POC

- Does native Delta SELECT work with old HMS physically unavailable?
- Does Spark-write -> Trino-read work?
- Does Superset metadata discovery work?

## Gate C — Production readiness

- Are failure modes safe?
- Are UC outages distinguishable from not-found?
- Is pagination complete?
- Are HA and load tests passed?
- Are all writes rejected?
- Is rollback proven?

Only then proceed to HMS decommission.

---

# 42. Final Engineering Principle

Keep the custom component as small as possible.

The adapter should answer:

```text
What schemas exist?
What tables exist?
Is this a Delta table?
Where is this Delta table?
```

Then get out of the way.

Trino should continue doing what it already does well:

```text
read _delta_log
resolve Delta snapshots
understand Delta protocol
prune files
read Parquet
execute SQL
```

Unity Catalog should remain the authoritative metadata catalog.

That separation is the core of the design.
