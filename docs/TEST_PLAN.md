# Test and certification plan

Status: acceptance specification, **not executed results**. Implement tests with the approved code. POC target is Trino 472 / UC 0.5.1 / Spark 3.5.9 Scala 2.12 / Delta 3.3.2 / UC Spark connector 0.2.1; certify 483 separately. Driver and Privacera deployment versions are still required.

## Layered tests

| Layer | Cases | Required assertion |
|---|---|---|
| Config/unit | Missing namespace/token, invalid URL/TLS, zero/negative limits, inconsistent deadlines, secret redaction, token-file replacement | Startup fails with actionable safe error; secrets never printed; no silent insecure default |
| UC HTTP client | Correct base path/query encoding; GET schema/table; auth header; cross-origin redirect; unknown JSON fields; missing identity, wrong namespace, bad format/location; body cap | Correct requests, bounded bodies/deadlines, no credential leak or mutation request |
| Pagination | >100 schemas, >50 tables; zero entries plus token; full final page plus empty terminal page; token cycles; duplicate names/conflicting data; concurrent rename/drop; limits; failure on page 2+ | All pages visited; legitimate empty page doesn't terminate; no partial list/cache on failure; bounded best-effort enumeration |
| Translation | Missing sd/SerDe/list negative fixtures; empty **non-null** cols/partitionKeys; empty maps/optional fields; unchanged S3 path with spaces/escapes/trailing slash; provider override attempts; malformed UC records | Actual Java converter accepts valid objects and rejects broken ones; exact provider/path and EXTERNAL_TABLE; no blind property or column forwarding |
| Scope | Delta external, managed Delta, non-Delta external, metric/materialized/streaming view, unknown enum; direct get versus list | Lists expose only eligible tables; ineligible direct lookup fails explicitly; invalid metadata never silently disappears |
| Thrift contract | Every initial RPC success/not-found/MetaException; request capabilities; catalog-name mismatch; unknown optional fields; method result field IDs; sequence IDs; multi-request socket; fallback/reconnect | Decode with actual 472/483 JVM client, then run bridge/Delta conversion; no same-generator-only proof |
| Dispatch rejection | Generate all methods from pinned IDL; call every non-allowlisted method including writes, locks, transactions, statistics and grants | Non-success application/declared error, zero UC write calls and zero side effects. Unknown future method also rejected |
| Networking/concurrency | Slow clients, idle sockets, malformed binary/container lengths, truncated requests, oversized messages, disconnect mid-page, overload, many connections, shutdown | Bounded connections/memory/goroutines; no cross-request identity/data leakage; cancellation and resource cleanup; race detector clean |
| Observability | RPC/upstream/failure spans and metrics; unknown-method flood; sensitive strings in upstream errors | Stable bounded labels; no secrets/payloads; local trace hierarchy works; no claimed automatic Trino trace propagation |

Expected RPCs/exception unions: [RPC_MATRIX.md](RPC_MATRIX.md). Expected object shapes: [UC_HMS_MAPPING.md](UC_HMS_MAPPING.md).

## Golden HMS contract oracle

Before retirement, collect sanitized responses from existing HMS 3.1.3 and UC for the **same** external Delta tables. Preserve response-field presence, provider, original location and type. Record source versions/configuration. Store fixtures under `integration/golden/` when implemented, without JWTs, user secrets or sensitive real paths.

Compare semantic compatibility through Trino's real converter/Delta wrapper, not byte equality. Adapter columns are intentionally empty while the old HMS may contain UC/Spark/dummy columns; this difference must not change DESCRIBE/SELECT. Compare table metadata/SQL output with expected Delta schema read independently via Spark. Test negative fixtures missing sd, SerDe, cols/partitionKeys, provider and path. Snapshot generated-wire objects and exception variants with canonical ordering only for test output; map serialization order is not the contract.

## Trino SQL coverage and RPC capture

Run each [call-flow operation](TRINO_CALL_FLOW.md#operations-required-by-the-poc) with cold and warm metastore caches and record method name, attempt count, status and latency. At minimum:

```sql
SHOW SCHEMAS FROM uc_delta;
SHOW TABLES FROM uc_delta.raw_bdp;
DESCRIBE uc_delta.raw_bdp.account_info;
SHOW CREATE TABLE uc_delta.raw_bdp.account_info;
SELECT * FROM uc_delta.information_schema.schemata;
SELECT * FROM uc_delta.information_schema.tables WHERE table_schema = 'raw_bdp';
SELECT * FROM uc_delta.information_schema.columns WHERE table_schema = 'raw_bdp';
SELECT * FROM uc_delta.information_schema.columns
 WHERE table_schema = 'raw_bdp' AND table_name = 'account_info';
SELECT * FROM uc_delta.raw_bdp.account_info;
SELECT count(*) FROM uc_delta.raw_bdp.account_info;
SELECT * FROM uc_delta.raw_bdp.account_info WHERE event_date = DATE '2026-09-01';
SHOW CREATE SCHEMA uc_delta.raw_bdp;
```

Use controlled fixture column names. Add projection, joins, aggregates, empty Delta table, nested types, comments, missing schema/table, LIKE filtering, unsupported object types and catalog-wide information_schema. Compare rows with Spark and the temporary old-HMS catalog against a fixed Delta version or quiescent writer; live concurrent writes otherwise invalidate naive equality comparisons.

Expected successful core RPC inventory is the three required methods, with get_table/get_database compatibility as described. Exercise get_table fallback deliberately using an injected first-call failure/unsupported request method. Assert get_fields, HMS statistics and partition APIs are absent from the normal Delta path. Investigate every unexpected method instead of adding the whole HMS API.

## Native Delta and Spark compatibility

| Scenario | Fixture/action | Pass criteria |
|---|---|---|
| Native writer/reader | Spark registers/creates external Delta through UC, writes N, Trino reads; Spark commits N+1 | Subsequent Trino query sees expected new rows within documented freshness; no adapter mutation or log parser |
| Existing locations | Register existing POC S3 tables; record UC path and object versions before/after | No data relocation, path normalization, Iceberg metadata or UniForm activation |
| Schema evolution | Add column; nullable/nested/decimal/timestamp types; UC column metadata deliberately stale; mapping-enabled rename/drop where supported | DESCRIBE, SHOW CREATE and SELECT follow Delta log, not stale UC cols or adapter cache |
| Partition pruning | Multiple partitions, null partition, selective and compound predicates, mapping-enabled partitions | Correct rows plus split/scanned-byte/file evidence that unselected partitions are pruned; zero HMS partition calls |
| Count optimization | With/without usable file statistics, deletion vectors, empty table and predicates | Same count as Spark; no dependency on Hive/UC row-count statistics |
| Checkpoints | JSON-only log, classic single/multipart checkpoints, long history and checkpoint+tail; V2 checkpoint separately | Native Trino reconstructs snapshot and returns correct rows; adapter protocol unchanged |
| Column mapping | none/name/id, physical/logical name mismatch, nested columns and rename | Correct values/types/pruning under supported combinations; native rejection of unsupported combinations |
| Protocol/features | Reader versions 1/2/3 and reader version >3; supported/unknown reader features; writer-enabled features separately | Compatible fixtures read correctly; unsupported fixtures fail/exhibit documented Trino rejection, never adapter format coercion |
| Time travel (if required) | Version/timestamp query before and after N+1, unavailable/vacuumed history | Correct retained snapshot or clear native failure; adapter has no snapshot cache |
| Catalog lifecycle | Spark/UC create/drop/rename where API supports it, missing parent, delete during pagination/query planning | List/get consistency bounded by documented caches; no fabricated location; no stale-on-error success |

The inspected 472 `DeltaLakeTableFeatures` allowlist contains columnMapping, timestampNtz, typeWidening/typeWidening-preview, deletionVectors, vacuumProtocolCheck, variantType/variantType-preview and v2Checkpoint; `getTableHandle` bounds reader version at 3. This is a **source allowlist, not certification of every combination** or proof Delta 3.3.2 can produce every feature. Build fixtures using supported native tooling, and negative cases using controlled test fixtures. Do not enable production features merely because their names appear here. [T-features](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/transactionlog/DeltaLakeTableFeatures.java) [T-metadata](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java)

## Superset and governance

Pin actual Superset, SQLAlchemy and trino-python-client versions. Reference source trace used Superset 6.1.0 and driver 0.340.0. Test schema dropdown, table/view dropdown, dataset import, column discovery including empty/nested tables, SQL Lab, representative charts, dashboards and concurrent discovery bursts. Capture emitted SQL and RPCs including has_table and SHOW COLUMNS fallback. Test cold/warm Superset caches and dataset refresh after schema evolution. [S-base](https://github.com/apache/superset/blob/6.1.0/superset/db_engine_specs/base.py) [S-trino](https://github.com/apache/superset/blob/6.1.0/superset/db_engine_specs/trino.py) [P-dialect](https://github.com/trinodb/trino-python-client/blob/0.340.0/trino/sqlalchemy/dialect.py)

Use at least allowed and denied users plus masking/row-filter policies. Verify Privacera enforcement, table/column visibility, audit attribution and S3 policy behavior; UC audit subject is the service identity by design. Probe direct adapter network access from an unauthorized workload and prove denial. Test setUGI unexpectedly enabled: it must fail clearly, not claim secure delegation.

Deny all Trino data mutations and DDL: INSERT, UPDATE, DELETE, MERGE, CTAS, CREATE/DROP/ALTER table/schema/view, comments, ANALYZE, OPTIMIZE, VACUUM/register/unregister or other available procedures, and policy-changing grants/session properties. Assert **no S3 object/Delta version changes and no UC mutations**, not only that a Thrift mutation failed. Keep `delta.metastore.store-table-metadata=false`; also intentionally enable it in a negative test to show scheduler mutation attempts are rejected and observable. [T-config](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeConfig.java) [T-scheduler](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/DeltaLakeTableMetadataScheduler.java) [T-security](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeSecurityModule.java)

## Failure matrix

| Fault | Check |
|---|---|
| UC 404 object / configured catalog / wrong route | Object GET not-found only for validated missing object; catalog/config/HTML 404 is failure |
| UC 401 / 403 / token rotation | No blind adapter retries; no false absence; restricted principal list filtering understood; both table RPC alternatives fail consistently |
| UC 409 / 429 / 5xx | Safe MetaException category; Retry-After bounded; outer Trino retry amplification measured |
| UC connection refused, DNS failure, timeout, TLS failure | Deadline/resource bounds; TLS errors never bypass verification; no partial/empty success |
| Malformed/oversized body, wrong names/location | Invalid-response error and no cache insertion |
| Failure on later pagination page | Entire list fails; no previous-page result reported as complete |
| Adapter crash/restart/overload/rolling replacement | Trino can reconnect or gets bounded explicit failure; no state recovery dependency |
| S3 403/unavailable/throttle; missing/corrupt log/checkpoint/Parquet | Native Trino error distinguished from adapter/UC errors; no adapter S3 retry/parser |
| Mixed per-table UC/S3 failure during columns discovery | Record stock Trino omission behavior and compare DESCRIBE/SELECT failures. Do not mark silent incomplete columns as complete discovery; require operational visibility and user acceptance |
| Unsupported Delta reader feature | Distinguish native no-handle/not-found-looking behavior from an actual UC 404 |

For every failure assert actual Thrift exception class, Trino error/output, RPC/UC attempt counts, latency, metrics, cache contents and resource cleanup. Stock information_schema tolerance is documented in [TRINO_CALL_FLOW.md](TRINO_CALL_FLOW.md#error-and-side-effect-boundaries); the adapter must still return the correct error.

## Concurrency, load, HA and rollout

Start with 100+ concurrent planning/discovery operations, then increase to forecast peak and burst load. Include large namespaces, slow UC, repeated table gets, many distinct tables and Superset dashboard fan-out. Record p50/p95/p99 RPC/UC/query-planning latency, throughput, CPU/memory/GC/goroutines, socket count, queue rejection, UC QPS and retry amplification. Agree SLOs and largest supported listing size **before production**; do not invent a pass threshold after a run.

Test 2+ replicas, pod eviction/PDB, rolling mixed adapter versions, readiness removal before drain, active sockets at termination, token/CA rotation and recovery from UC outage without liveness restart loops. Verify stateless restart and load distribution. If cache is introduced, repeat rename/drop/location-change and permission revocation tests against combined Trino+adapter TTLs, and prove no persistent state is required.

Canary via separate Trino catalog/Superset datasource, compare fixed snapshots, drill rollback by routing back without moving data. Document operational commands, dashboards, alerts and escalation ownership in RUNBOOK during implementation, not as fictitious current capabilities.

## Mandatory HMS retirement gate

1. Prepare clean Spark and Trino processes with catalog configs recorded; clear caches and include a newly registered UC table so warm caches cannot fake success.
2. Stop old HMS **or network-block all paths to it** from both Spark and Trino. Keep the block throughout the complete POC suite, N+1 write/read, discovery/load and rolling tests. Prove the block itself with a deliberately failing old-HMS connection attempt.
3. Record network/flow/connection evidence: old HMS traffic zero from target workloads; HMS database has no target-workload access. Inspect Spark SessionCatalog/Hive settings too, not just Trino metastore URI.
4. Run all required SQL, Spark native write/read, real Superset flows and governance tests. Verify no fallback catalog, no Iceberg/UniForm metadata and unchanged S3 table locations.
5. Record image/config/source versions, request inventory, expected/actual results and artifact links. Production retirement additionally requires the agreed stability observation window and rollback ownership.

A live HMS oracle may be used for earlier comparisons, but it cannot remain reachable during this gate. Passing tests with HMS merely idle or with already cached table handles is insufficient.
