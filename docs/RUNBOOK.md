# Native Delta SELECT POC runbook

This runbook is for the existing S3 table and official Trino 472 Delta connector.
It is an execution procedure, not a record of passed tests. Current status is in
[MILESTONE_4.md](MILESTONE_4.md). No production optimization is part of this test.

## Establish the baseline

Record UTC timestamps, exact Trino/UC/Spark/Delta versions, image digests, adapter
build/configuration and service identities with secrets removed. Inspect the
active Trino catalog: connector must be `delta_lake`, metastore URI must name only
the adapter, native HMS catalog override/impersonation must be unset/disabled, and
metadata write-back must remain disabled. Preserve deployment governance and
Trino's existing S3 credentials. Confirm the running Trino image is official and
unmodified; a local configuration file alone is not proof of deployed settings.

Retrieve the actual UC TableInfo. Record UC catalog/schema/table, external DELTA
type, table identity and exact storage_location. Confirm the table already exists
at that S3 location. Inspect Delta protocol/metadata with existing Spark tooling
and record version N, schema, partition columns and table properties. Verify no
Iceberg catalog/redirection or UniForm flow is active. Coordinate a quiescent writer
window so an unrelated commit cannot be mistaken for the intended N+1 write.

Obtain baseline expected rows, count and partition-result set using Spark at
version N. Agree the exact N+1 data change and unique marker before writing. Keep
result/evidence files in the approved local evidence directory with restricted
access; query results can contain real table data. Do not commit credentials or
unsanitized query output to Git.

## Enforce HMS isolation before measurements

Identify every address/port/alias and workload for the old HMS, including any
fallback endpoint. Stop that service or apply an explicit network block covering
Trino coordinators/workers, adapter and Spark driver/executors. Record the actual
applied rule/service state and scope, not only an intended manifest.

From each relevant client network, demonstrate a failed old-HMS connection after
the block. Mark these intentional negative probes separately from the measured
query window. Capture destination-aware connection/flow telemetry and old-HMS
request/audit logs for the whole test. Adapter RPC counters alone cannot prove
that Trino or Spark did not contact another HMS.

Retain the block through all reads, the Spark commit and post-commit reads. Use
fresh/cold Trino metadata state with the active configuration recorded so existing
cached handles cannot hide an old-HMS dependency. Start a fresh Spark writer
session with UC catalog settings recorded and no Hive fallback. Reconfirm the
block and telemetry coverage at the end.

## Execute and capture reads at N

Use the actual identifiers and an agreed partition predicate; do not execute the
placeholder SQL literally. Run discovery first, then all requested reads:

```sql
SHOW SCHEMAS FROM uc_delta;
SHOW TABLES FROM uc_delta.<schema>;
DESCRIBE uc_delta.<schema>.<table>;
SELECT * FROM uc_delta.<schema>.<table>;
SELECT count(*) FROM uc_delta.<schema>.<table>;
SELECT <columns> FROM uc_delta.<schema>.<table>
WHERE <partition_column> = <typed_partition_value>;
```

For every query record query ID, start/end UTC, rows/count or error, wall time,
Trino execution/planning statistics and physical input/splits where available.
Compare unordered SELECT results as multisets against Spark's version-N baseline;
record duplicates and nulls correctly. Do not rely solely on count equality.
Record cold versus warm runs separately. A predicate returning expected rows is
not by itself proof of partition pruning; capture split/file/physical-input
observations too. Count queries may use Delta statistics; capture log and Parquet
reads across the full suite rather than assuming every query reads Parquet.

Enable the adapter's existing debug RPC logs and capture metrics before/after the
isolated test window. Record the actual RPC inventory and outcomes; expected reads
are the five approved methods, with request/legacy table lookup alternatives.
Capture UC access/audit evidence for the exact GET/list paths and service identity.
Correlate returned storage_location with the adapter's verified metadata contract.
No UC mutation or adapter metadata edit is needed for these reads.

Capture Trino query/task or storage diagnostics plus S3 data-access evidence for
`_delta_log` and Parquet keys under the unchanged prefix, using the Trino storage
identity. If available, capture runtime spans/stacks identifying TransactionLogAccess.
Do not claim that a logger name or source-code diagram proves a class executed.
Timestamp/counter correlation is sufficient only in an isolated window with no
unrelated traffic; otherwise use the deployment's request/query correlation.
Do not enable payload/JWT logging to obtain this evidence.

## Write N+1 with Spark and verify freshness

1. With HMS still blocked, perform only the agreed Spark data change through the existing native Delta writer. Retain the UC registration and S3 location. Do not overwrite, relocate, enable UniForm or add Iceberg metadata.
2. Capture Spark job ID/output and Delta history showing N → N+1, commit timestamp, operation and marker. If another writer intervened, reconcile actual versions and rerun a controlled window; do not label an arbitrary latest commit N+1.
3. Capture expected post-commit rows/count/partition results independently through Spark. Confirm UC table identity/location remain unchanged.
4. Start a new Trino statement/transaction and rerun SELECT *, count and the partition query. Do not restart/reconfigure/update the adapter or alter its returned metadata. Record the first post-commit attempt, then any bounded polling interval and visibility delay; never hide initial stale results.
5. Verify both the unique changed data and expected aggregate/partition changes. Capture Trino reading the new Delta log version or equivalent runtime snapshot evidence. A coincidentally correct count alone does not prove N+1 visibility.
6. Record no manual adapter updates, unchanged UC storage_location and no old-HMS access throughout the combined window. Cleanup, if requested, is a separate authorized Delta commit after evidence capture; it is not part of N+1.

## Verdict

Fill [MILESTONE_4.md](MILESTONE_4.md) with links to actual artifacts, query results,
RPC inventory, call trace, HMS isolation/zero-request evidence, N/N+1 comparison,
latencies/freshness delay and remaining blockers. PASS requires all hard conditions.
Missing access or telemetry is a failed acceptance gate, not evidence of query
failure and not permission to fabricate a PASS. Stop after the POC; do not add
caches, performance tuning, optional RPCs or production rollout changes.
