# HMS RPC contract

Target: Trino 472 TCP Thrift client, UC v0.5.1, external Delta only, one UC catalog per adapter deployment, no Hive redirection, no impersonation, no HMS catalog-name override. Source-derived on 2026-09-25; not yet traffic-certified. All methods use the field IDs and exception result slots from [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift).

## Actual implementation status — Milestone 3

| RPC | Implemented behavior | Verification |
|---|---|---|
| `get_all_databases` | UC schemas for configured catalog, complete bounded pagination, identity/duplicate checks | Unit + TCP contract |
| `get_database` | UC schema → HMS Database; declared schema not-found | Unit + TCP contract |
| `get_table_meta` | UC tables for configured catalog/exact schema, page size 50, complete pagination; external DELTA only; table pattern `*`; type filtering | 51-table/empty-page unit test + TCP contract |
| `get_table_req` | UC exact lookup → synthetic HMS Table wrapped in GetTableResult; capabilities accepted; explicit HMS `catName` rejected | Golden TCP contract + independent field-ID/container checks |
| `get_table` | Same lookup, mapping and failure behavior as request RPC; compatibility path | Golden TCP contract + error parity tests |
| `get_tables`, `get_tables_by_type` | Not implemented: alternate client path, not required by selected TCP contract | UNKNOWN_METHOD inventory test |
| `get_fields` | Not implemented: Delta DESCRIBE uses `_delta_log`; synthesized metadata is not Avro/CSV | UNKNOWN_METHOD inventory test |
| All other methods, including writes | Explicit UNKNOWN_METHOD; no HMS fallback or UC mutations | Complete upstream inventory rejection test |

Eligible means exactly UC `EXTERNAL` + `DELTA`. Known out-of-scope objects are
filtered from lists and rejected on lookup; malformed/unknown records fail.
Provider is `DELTA`, and unchanged UC location populates both SD location and
SerDe `path`. Empty HMS columns/partition keys preserve Delta-log authority.

Exact table lookup maps UC `TABLE_NOT_FOUND` or missing parent `SCHEMA_NOT_FOUND`
404 to NoSuchObjectException. Catalog/generic/proxy 404s stay configuration errors.
Only a **first-page** table-list `SCHEMA_NOT_FOUND` 404 permits an empty list;
later-page failures abort with no partial results. Complete valid empty lists
succeed. Outages, timeouts and malformed metadata never appear as empty discovery.

Live `SHOW TABLES` and `DESCRIBE` acceptance remains unverified under the owner's
earlier Trino integration deferral. Golden fixtures are synthetic; no sanitized
real-HMS response was available. The actual implemented mapping and tests are
recorded in [Milestone 3](MILESTONE_3.md); the broader contract below is design
intent wherever not implemented above.

## Minimum and compatibility scope

**Successful normal-path minimum for the 13 requested operations: three RPCs**: `get_all_databases`, `get_table_meta`, `get_table_req`. View probes/enumeration reuse table lookups/lists. `get_database` is not the schema-existence implementation. It is used for schema properties (`SHOW CREATE SCHEMA`) and write paths. [T-spi](https://github.com/trinodb/trino/blob/472/core/trino-spi/src/main/java/io/trino/spi/connector/ConnectorMetadata.java) [T-view](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/TrinoViewHiveMetastore.java) [T-metadata](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java)

**Recommended first implemented contract: five RPCs**: the three above plus `get_table` and `get_database`. The former covers Trino's real alternative-call behavior, including initial failures; the latter provides useful schema metadata and the handoff's intended database response. This is an intentional two-method compatibility margin, not evidence that every listed query calls all five. Defer other successful read methods until an observed client needs them.

REQUIRED = normal target path; COMPATIBILITY = identified alternate/adjacent path; OPTIONAL = absent from target path, defer; VERIFY = depends on unknown deployment behavior; MUST_REJECT_WRITE = must never mutate state or report successful mutation.

| RPC / family | Classification | Evidence and contract |
|---|---|---|
| `get_all_databases` | REQUIRED | Client `getAllDatabases` without catalog override. Return all authorized UC schema names after complete pagination. [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_databases` | COMPATIBILITY | With `hive.metastore.thrift.catalog-name`, client sends an encoded pattern (catalog marker `@`, separator `#`, empty marker `!`). This is **not** the UC catalog mapping. Leave setting unset. Implement HMS pattern semantics and catalog validation only if enabled later. [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_database` | COMPATIBILITY (include in first five) | D chain in call-flow document. GET schema → Database. Typed not-found for absent schema. [T-wrapper](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/HiveMetastoreBackedDeltaLakeMetastore.java) [T-metadata](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java) |
| `get_table_req` | REQUIRED | `GetTableRequest.dbName/tblName`; Trino includes `INSERT_ONLY_TABLES` capability. Validate optional `catName` against supported configuration; capability is not permission to write. Return `GetTableResult{table}`. [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift) |
| `get_table` | COMPATIBILITY (include in first five) | Alternative to request RPC. Same translation and upstream GET. Alternative selection persists in client-factory shared state. Both must fail consistently on UC outage. [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_table_meta` | REQUIRED | TCP factory sets support true. Trino sends literal schema, table glob `*`, empty type list; special schema names containing `\|`/`*` take a pattern branch. Return external-Delta TableMeta only. [T-client-factory](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/DefaultThriftMetastoreClientFactory.java) [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_tables` | COMPATIBILITY | 472 HTTP-oriented client construction disables table-meta support and uses `get_tables(db,".*")`; this is not automatic fallback from an RPC exception. Not needed for the selected TCP endpoint. [T-http-factory](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/HttpThriftMetastoreClientFactory.java) [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_tables_by_type` | COMPATIBILITY | Same alternate path calls type `VIRTUAL_VIEW`. External-only contract has no views. Implement only alongside an approved alternate client; do not return fake views. [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_all_tables`, `get_table_objects_by_name[_req]` | OPTIONAL | Present in IDL, not used by the verified normal Delta discovery chain. Do not add batch/alternate APIs speculatively. [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift) [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_fields` | OPTIONAL | Bridge invokes only for Avro-with-schema metadata. Our synthesized table must never identify as Avro/CSV. Delta DESCRIBE and Superset columns use log-derived metadata, not this RPC. If added, contract needs `UnknownTableException`/`UnknownDBException`, not undeclared NoSuchObject. [T-bridge](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/BridgingHiveMetastore.java) [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift) |
| `get_table_names_by_filter` | OPTIONAL | Client supports parameter-filter queries, but Delta metastore interface does not call it; Trino view enumeration uses `getTables` and TableMeta. No general HMS expression parser needed. [T-interface](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/DeltaLakeMetastore.java) [T-view](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/TrinoViewHiveMetastore.java) [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| `get_table_statistics_req` / partition statistics reads | OPTIONAL | No Delta query/statistics path requires them. Delta uses `FileBasedTableStatisticsProvider`. Unsupported is safer than fabricated zero statistics. [T-stats](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/statistics/FileBasedTableStatisticsProvider.java) |
| Partition reads (`get_partition*`, `get_partitions*`, partition-name/filter APIs) | OPTIONAL | Native Delta splits use transaction log actions; never populate an HMS partition catalog. [T-splits](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeSplitManager.java) |
| `set_ugi` | VERIFY; not enabled initially | `UgiBasedMetastoreClientFactory` calls `setUGI` before ordinary RPCs if impersonation enabled; client sends username + empty groups. This is untrusted identity metadata, not UC JWT delegation. Reject unexpected use initially; do not silently claim impersonation. [T-ugi](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/UgiBasedMetastoreClientFactory.java) [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) |
| Role/privilege reads (`get_role_names`, `list_roles`, `get_role_grants_for_principal`, `get_principals_in_role`, `get_privilege_set`, `list_privileges`) | VERIFY | Not needed by the pinned Delta read-only connector contract. Delta security modes are ALLOW_ALL/READ_ONLY/FILE/SYSTEM, not Hive SQL-standard authorization. Privacera/system plugin deployment may add dependencies and requires tracing. No fabricated grants. [T-security](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeSecurityModule.java) [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift) |
| Role/privilege mutations (`create_role`, `drop_role`, `grant_role`, `revoke_role`, `grant_revoke_role`, `grant_revoke_privileges`) | MUST_REJECT_WRITE | Never treat absence of an authorization implementation as allow. |
| Database mutations (`create_database`, `alter_database`, `drop_database`) | MUST_REJECT_WRITE | No UC mutations. |
| Table/view mutations (`create_table*`, `alter_table*`, `drop_table*`, truncate/constraint APIs) | MUST_REJECT_WRITE | Covers rename, schema/comment changes, metadata scheduler writes and registered views. |
| Partition mutations (`add_partition*`, `append_partition*`, `alter_partition*`, `drop_partition*`, `exchange_partition*`) | MUST_REJECT_WRITE | No partition creation or mutation. |
| Statistics mutations (`update_table_column_statistics`, `update_partition_column_statistics`, `delete_*column_statistics`, `set_aggr_stats_for`) | MUST_REJECT_WRITE | No successful no-op writes. |
| Transaction/lock/write-ID APIs (`open_txns`, `commit_txn`, `abort_txn*`, `allocate_table_write_ids`, `lock`, `unlock`, heartbeat) | MUST_REJECT_WRITE | No fake transaction IDs, locks or HMS ACID state. |
| Catalog/function/constraint/notification/resource-plan mutations; future unrecognized methods | MUST_REJECT_WRITE for mutations; otherwise unsupported | Positive allowlist at dispatch. Nothing falls through to old HMS or UC writes. |

The mutation family patterns are policy coverage, not assertions that every spelling exists in tag 2. Generate a complete method-name inventory from the pinned IDL in the protocol milestone and test every non-allowlisted method for failure.

## Patterns, missing objects and scope

For initial `get_table_meta`, implement the actual target forms: exact ordinary schema name, table pattern `*`, type list empty (all eligible types) or containing `EXTERNAL_TABLE`. Return `[]` for a supported type restriction with no matches. Defer broader HMS glob/regex behavior; reject unsupported patterns with MetaException rather than interpreting arbitrary regex or returning wrong matches. If existing names require Trino's `|`/`*` branch, expand this contract **before** implementation. Name/case inventory is an approval gate.

A missing exact schema during table listing can yield an empty result only when UC reports a verified `SCHEMA_NOT_FOUND` for that schema. Missing configured catalog is configuration failure, never an empty catalog. A failure on any later page aborts the list; do not send earlier entries. Lists carry no pagination token on the HMS wire, so the adapter must aggregate within a bounded deadline/size budget.

## Wire error contract

UC OpenAPI lists only successful responses for these reads; failure and auth behavior below are verified against server code, not invented OpenAPI declarations. `GlobalExceptionHandler` emits `error_code`, `message`, `details` and optionally stack traces; discard upstream stack traces from downstream errors. [U-api](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/api/all.yaml) [U-handler](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/exception/GlobalExceptionHandler.java) [U-base-handler](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/exception/BaseExceptionHandler.java) [U-errors](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/exception/ErrorCode.java)

| Upstream result | Adapter result | Retry within adapter |
|---|---|---|
| Table/schema GET: 404 with matching `TABLE_NOT_FOUND` / `SCHEMA_NOT_FOUND` (or validated object-level NOT_FOUND) | `NoSuchObjectException` for `get_table[_req]` / `get_database` | No |
| 404 for configured catalog, wrong base route, HTML proxy error, unknown origin | `MetaException("UC_CONFIGURATION_ERROR: ...")`; not object absence | No |
| First-page list 404: verified missing queried schema | Empty table list permitted; missing configured catalog fails | No |
| 401 | `MetaException("UC_AUTHENTICATION_FAILED: ...")` | No blind retries; rotate token operationally |
| 403 | `MetaException("UC_AUTHORIZATION_FAILED: ...")` | No |
| 409 on a read | `MetaException("UC_CONFLICT: ...")`; unexpected read state, not AlreadyExistsException | No |
| 429 | `MetaException("UC_THROTTLED: ...")` after bounded attempts; respect Retry-After within total deadline | Yes, bounded |
| 5xx | `MetaException("UC_UNAVAILABLE: ...")` | Yes, transient statuses only |
| Connection refused/reset/DNS error | `MetaException("UC_UNAVAILABLE: ...")` | Bounded for transient transport failure |
| Timeout / deadline | `MetaException("UC_TIMEOUT: ...")` | Only if total budget remains |
| Malformed JSON, invalid required identity/location, wrong namespace, oversized response | `MetaException("UC_INVALID_RESPONSE: ...")` | No |
| Partial pagination failure, token cycle or page/entry budget exhausted | `MetaException("UC_LIST_FAILED: ...")`, no partial success/cache | Retry failing GET only within remaining budget; otherwise fail whole operation |
| Known non-Delta, managed or view-like object outside initial scope | `MetaException("UNSUPPORTED_TABLE: ...")` on direct lookup; excluded from lists | No |
| Any mutation / unsupported RPC | Protocol-level `TApplicationException(UNKNOWN_METHOD)` with explicit unsupported/read-only message from dispatch; no success result, no side effects | No adapter retry |

Read APIs in the initial five declare MetaException; object GETs also declare NoSuchObjectException. `get_table_meta` declares **only MetaException**, so do not serialize a NoSuchObject result slot there. `get_fields`, if ever enabled, declares MetaException, UnknownTableException and UnknownDBException. [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift)

Trino `ThriftHiveMetastore.getDatabase/getTable` stops on NoSuchObjectException and returns `Optional.empty`; other TExceptions eventually become `HIVE_METASTORE_ERROR`. Reads generally retry MetaException. Before get-table alternative selection settles, the client may reconnect and attempt `get_table` after **any** exception other than NoSuchObjectException or text containing `AccessControlException`. Do not assume MetaException is nonretryable and do not spoof an internal Java exception name to control retries. Budget the outer Trino retry loop and both alternatives; auth 401/403 must remain errors across both. [T-thrift](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastore.java) [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) [T-retry](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/RetryDriver.java)

Stock information-schema column listing may suppress the resulting runtime error; see [call-flow limitations](TRINO_CALL_FLOW.md#error-and-side-effect-boundaries). This is a caller limitation, not permission for the adapter to convert an outage into absence.
