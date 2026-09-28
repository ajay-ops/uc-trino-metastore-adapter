# Risks, blockers and decisions

Review date: 2026-09-25. Source supports the adapter architecture; runtime certification is outstanding. No live deployment was examined.

## Hidden dependencies and limits

| Risk / finding | Consequence | Required action / gate |
|---|---|---|
| Trino writes can bypass HMS via native Delta/S3 | Adapter read-only RPCs do not enforce read-only SQL | Explicit Trino/Privacera deny policy; test all DML/DDL/maintenance and verify S3 unchanged |
| Metadata write-back can run during reads if enabled | Unexpected alter-table RPCs after SELECT/discovery | Explicit `delta.metastore.store-table-metadata=false`; negative test scheduler behavior |
| Stock information_schema.columns tolerates per-table runtime errors | UC/S3 outage can produce incomplete discovery despite correct adapter errors | Accept/document this caller behavior with monitoring and strict DESCRIBE/SELECT probes, or reconsider the no-fork constraint if strict atomic discovery is mandatory |
| Unsupported Delta features can yield no table handle | An existing UC table can look missing to Trino | Inventory actual protocol/features and certify native reader; do not blame/change catalog mapping |
| UC principal is shared | UC does not enforce end-user policies through this bridge | Validate Privacera/system enforcement and direct adapter isolation; no setUGI identity claims |
| v0.5.1 JWT verifier requires UC INTERNAL issuer and enabled subject | Generic external JWT may fail | Confirm service account/token provisioning, least privilege, issuer and rotation |
| UC list authorization filtering occurs after paging | Empty page may still contain next token; service-visible lists differ from user-visible lists | Complete pagination + governance visibility tests |
| No transactional snapshot across UC list pages | Concurrent rename/drop can change enumeration | Define bounded best-effort semantics; fail on page errors, never silently truncate |
| Names/case/special characters | UC names might collide after Trino lowercase handling or invoke HMS pattern branches | Inventory production identifiers; reject unsupported ambiguities; do not normalize silently |
| Managed/catalog-owned tables | May require commit/credential semantics not supplied by external-table adapter | Initial external-only scope; separate design before enabling managed tables |
| UC SDK/Spark version skew | 0.2.1 clients against 0.5.1 server not certified by source trace | Real Spark 3.5.9/Delta 3.3.2 create/write/N+1 test with HMS blocked |
| Spark SessionCatalog / redirection configuration | Hidden old-HMS dependency survives adapter migration | Cold-start Spark+Trino with network isolation; no Hive fallback catalog |
| Trino alternate calls and retries | A single UC failure can multiply connections/GETs and latency | Implement get_table consistently, tune combined budgets, load/fault test |
| Empty HMS columns | Source accepts shape, but generated wire contract not exercised | Mandatory JVM conversion test before implementing higher layers |
| Go runtime not installed/tested | No build or generated interop validation yet | Pin/install toolchain in P0; compile/race/wire CI gates |
| Privacera/plugin and Superset versions unknown | Extra SQL/RPC, Java/plugin compatibility and governance behavior unresolved | Pin actual versions and capture traffic; reference-source traces are conditional |
| Rollback requires temporary old catalog | Old HMS cannot be decommissioned before rollback/stability gates | Separate parallel validation, isolation test and final decommission stages |

Evidence: [T-metadata](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java) (getTableHandle/streamTableColumns/write scheduling), [T-manager](https://github.com/trinodb/trino/blob/472/core/trino-main/src/main/java/io/trino/metadata/MetadataManager.java) (listTableColumns/handleListingError), [T-scheduler](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/DeltaLakeTableMetadataScheduler.java), [T-client](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftHiveMetastoreClient.java) (alternativeCall), [T-convert](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftMetastoreUtil.java), [U-auth](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/AuthDecorator.java), [U-schema](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/SchemaService.java), [U-table](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/TableService.java), [U-page](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/persist/utils/PagedListingHelper.java).

## Decisions requiring the owner

1. **Approve the implementation contract?** Recommend five initial RPCs (`get_all_databases`, `get_table_meta`, `get_table_req`, `get_table`, `get_database`), external Delta only, one UC catalog per adapter deployment, empty HMS column/partition lists, no adapter cache initially. The strict normal-path minimum is three; the other two are deliberate compatibility support.
2. **Accept stock information-schema partial-discovery behavior?** Correct errors are still mandatory, but strict all-or-nothing column discovery cannot be guaranteed by this adapter with unmodified Trino. Recommend retain official connector and make incomplete discovery observable; define the operational response.
3. **Production target?** Recommend certify current Trino 483 alongside POC 472; confirm the intended UC production version and Privacera/Java/plugin support before switching. UC remains 0.5.1 in this plan.
4. **What is actually deployed?** Supply Superset, SQLAlchemy/trino-python-client, Privacera plugin/version, Trino security mode, any impersonation/HMS catalog override/redirection, Spark catalog config and UC auth setup. Reference Superset 6.1.0/driver 0.340.0 are not assumed deployment versions.
5. **Namespace/data scope?** Confirm UC catalog(s), whether any managed/view-like tables must be read, case/special-character inventory, existing S3 schemes and Delta reader features. Recommend external-only admission initially; mixed table listings are filtered, direct unsupported gets fail explicitly.
6. **Identity/transport?** Who provisions and rotates the UC service token, which least-privilege grants cover the intended tables, and where will TLS/mTLS terminate on both connections? Confirm whether Privacera enforces in Trino rather than depending solely on HMS hooks.
7. **Operational targets?** Expected schemas/tables/QPS/concurrency, p95/p99 planning budget, allowable metadata staleness, largest listing, rollout window and HMS isolation observation period; nominate rollback/security owners.

Decisions 1–2 affect the implementation contract immediately. The others can be resolved during the staged POC where independent work permits, but are production release gates. No permission to start production implementation is inferred from this document.

## Engineering verification still required (not user design decisions)

- Compile generated Go with a pinned supported toolchain and exact projected IDL; prove decoding/conversion using actual 472 and 483 Java clients.
- Trace every required SQL/Superset path with caches cold/warm and reconcile observed RPC counts with the source matrix; particularly views/probes, fallback, authorization hooks and statistics.
- Validate UC auth-enabled pagination against the deployed build, including pages filtered to zero and correct object-vs-route 404 classification.
- Confirm governance denies every S3-writing entry point and scheduler side effect.
- Test deadlines/admission/cancellation, large-list response size, outage behavior, token rotation, rolling upgrades and zero old-HMS connections.
- Review dependency/toolchain security status before pinning production images; source comparison does not equal vulnerability certification.

No proven architectural blocker requires a Trino fork today. **Strict atomic column discovery**, an HMS-only Privacera enforcement deployment, or a requirement for unsupported managed/protocol features could change that result. Escalate those requirements explicitly rather than silently widening the adapter.
