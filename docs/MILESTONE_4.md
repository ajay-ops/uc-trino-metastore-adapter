# Milestone 4: native Delta SELECT POC

## Current verdict

**DEFERRED by owner — not executed. PASS/FAIL runtime verdict is pending.**
No live query, HMS isolation change or Spark write was executed. After environment
inspection and preparation of the runbook, the owner requested skipping this
milestone for now. Missing environment inputs below remain prerequisites if the
owner resumes it. No adapter or Trino runtime failure was observed.

| Required evidence | Actual result |
|---|---|
| Official Trino Delta connector reads the existing UC-registered S3 table | Not run: target table, Trino endpoint and credentials/access not supplied |
| `SELECT *` | No query ID or rows captured |
| `SELECT count(*)` | No count captured |
| Partition predicate query | Partition column/value and expected result not supplied |
| Existing S3 location unchanged | Actual UC storage_location and S3 access not supplied; not verified |
| Old HMS stopped or network blocked | Old HMS endpoint/workload and administration access unknown; no block applied |
| Zero old-HMS requests | No workload/network/HMS telemetry captured; cannot claim zero |
| Spark commit N+1 | Spark execution access and intended data change not supplied; no write performed |
| Trino observes N+1 without manual adapter updates | Not tested |
| Latency | No measurements; not zero |
| RPCs invoked by live queries | None observed; no live queries executed |
| Iceberg/UniForm absent and Trino unmodified | Runtime configuration/images/table properties not available for inspection |

## Intended call trace (not runtime evidence)

```text
Trino official Delta connector
  -> adapter get_table_req (get_table compatibility alternative)
  -> UC GET /tables/{catalog}.{schema}.{table}
  <- UC storage_location
  <- HMS provider=DELTA; sd.location and SerDe path=storage_location
Trino TransactionLogAccess -> existing S3 prefix/_delta_log
Trino native Delta reader -> existing S3 Parquet files
```

UC returns the location; it is not a proxy for Trino's object reads. Expected
schema/table discovery may also invoke get_all_databases, get_database and
get_table_meta. Record the actual observed inventory; do not substitute this
source-derived expectation for a trace.

## Environment inspection

- Deployment ConfigMap contains `https://uc.example/api/2.1/unity-catalog`, not a usable target endpoint.
- No configured AWS or Kubernetes directories were found in the local account. No `aws`, `kubectl` or `spark-submit` executable was found on PATH.
- No usable Trino/Spark/UC connection profile or sanitized real-HMS fixture was found in the adapter repository. The local SSH configuration did not identify a POC host.
- The dedicated Colima test VM is stopped. A previously downloaded Trino 472 image alone cannot exercise the user's existing S3 table or existing Spark writer.
- No production code or optimization was changed. Existing Milestone 3 test results are not promoted to Milestone 4 evidence.

## Inputs required to proceed

1. Trino endpoint/access and exact UC catalog/schema/table; existing S3 storage_location; a partition predicate and baseline expected results.
2. UC endpoint and credential-file/profile reference, adapter deployment/access, Spark execution access and existing storage identity. Do not paste credentials into this document.
3. Old HMS endpoint/workload, access to stop/block it, affected client workloads, and network/HMS audit capture access.
4. The intended Spark append or other explicitly specified test data change, its expected effect, a stable row marker and cleanup requirements. This modifies the existing table and cannot be invented safely.
5. Runtime configuration/image versions and access to query/event, UC, adapter and S3 logs sufficient to correlate the complete test window.

[RUNBOOK.md](RUNBOOK.md) specifies the execution and evidence sequence. Populate
this report from actual artifacts after access is supplied; PASS requires every
hard condition and N+1 visibility to be demonstrated. Do not move the table, create
a substitute local dataset, add Iceberg/UniForm, patch Trino or manually update the
adapter to manufacture acceptance.
