# Milestone 1 completion report

Scope: infrastructure only. No UC schema/table APIs, metadata translation, successful HMS metadata reads, Delta access or Trino SELECT implementation was added.

## Implemented

- Validated environment configuration, including mounted bearer credentials, deadlines, bounded retries, pagination limits, disabled-cache settings and log level.
- Concrete UC GET client with verified TLS, token rotation, context cancellation, structured errors, bounded response bodies, transient-error retries and bounded pagination.
- Unframed binary Apache Thrift server with bounded connections, socket deadlines, explicit rejection of unsupported calls and bounded shutdown.
- Five generated read signatures return declared `MetaException`; all other upstream methods return `UNKNOWN_METHOD`. Unsupported calls never return successful empty metadata.
- JSON logs, Prometheus metrics, local liveness and infrastructure readiness. Readiness does not assert UC authentication or functional catalog reads.
- Non-root Dockerfile, Kubernetes configuration/deployment/service/probes/Secret reference/NetworkPolicy/PDB, build commands and CI.

## Files created

```text
.dockerignore
.github/workflows/ci.yml
.gitignore
Dockerfile
Makefile
go.sum
cmd/server/main_test.go
deploy/kubernetes/configmap.yaml
deploy/kubernetes/deployment.yaml
deploy/kubernetes/networkpolicy.yaml
deploy/kubernetes/pdb.yaml
deploy/kubernetes/service.yaml
docs/MILESTONE_1.md
integration/wire_test.go
internal/config/config.go
internal/config/config_test.go
internal/observability/observability.go
internal/observability/observability_test.go
internal/thrift/handler.go
internal/thrift/server.go
internal/thrift/server_test.go
internal/thrift/idl/hms.thrift
internal/thrift/generated/GoUnusedProtection__.go
internal/thrift/generated/hms-consts.go
internal/thrift/generated/hms.go
internal/unity/client.go
internal/unity/client_test.go
internal/unity/pagination.go
scripts/generate-thrift.py
third_party/hive-thrift/LICENSE
third_party/hive-thrift/README.md
third_party/hive-thrift/hive_metastore.thrift
```

Existing files updated: `go.mod`, `cmd/server/main.go`, `README.md`,
`CODEX_HANDOFF.md`, `docs/IMPLEMENTATION_PLAN.md`, `integration/README.md`,
`deploy/kubernetes/README.md`, and package comments in `internal/config/doc.go`,
`internal/observability/doc.go`, `internal/thrift/doc.go`, `internal/unity/doc.go`.
The existing cache and translation package placeholders remain unchanged.
The repository was entirely untracked at the start; this inventory distinguishes
new files from the original workspace rather than from a Git commit.

## Decisions and deviations from the approved plan

1. The owner's explicit Milestone 1 restriction takes precedence over the broader plan's working-handler and translation work. Five typed rejection stubs establish the wire boundary; successful RPCs, UC domain APIs and Java/Trino interoperability certification remain later work.
2. Selected and tested Go 1.27.1 and matching Apache Thrift compiler/runtime 0.24.0 instead of retaining the placeholder Go 1.22 or adopting the plan's candidate Thrift 0.22.0. Generated code is committed and reproducibility is checked.
3. The unchanged upstream IDL is `trinodb/hive-thrift` tag 2, the Trino 472 client artifact. Its SHA-256 and exact provenance are in `third_party/hive-thrift/README.md`. This artifact is not presented as the complete Apache HMS 3.1.3 API.
4. The deterministic IDL projection omits optional `StorageDescriptor.skewedInfo` (field 11): its upstream list-keyed map cannot compile in Go. No other retained wire field is retyped or renumbered. An independent wire fixture verifies skipping the original optional field. External Delta does not require it; arbitrary Hive skewed tables are outside this model.
5. Shutdown uses a per-server deadline and tracked socket closure instead of changing Apache Thrift's process-global `ServerStopTimeout`. Connection admission bounds simultaneous RPCs; UC connections are bounded per host. The runtime's accept loop is used directly so listener failures are returned.
6. Cache settings are validated, but nonzero cache TTL is rejected. No cache abstraction or allocation was added. OpenTelemetry spans, a dependency-health endpoint, domain-specific duplicate-identity checks and HMS TLS/SASL are deferred; these are not needed to satisfy the requested infrastructure milestone. Plain Thrift requires network isolation or later validated TLS termination.
7. Container/compiler image versions are pinned by tag, not immutable digest. Digest pinning and image availability/build validation remain deployment gates. Docker and kubectl are unavailable locally, so no container or cluster execution is claimed; CI includes the container build.

## Verification

Passed locally with Go 1.27.1 and Apache Thrift 0.24.0:

```text
go test ./...                       PASS
go test -race ./...                 PASS
go vet ./...                       PASS
go build -trimpath -o bin/server ./cmd/server  PASS
python3 scripts/generate-thrift.py --check     PASS
gofmt -l cmd internal integration   no output
```

Tests cover configuration failures, token rotation/invalid credentials, request cancellation and timeouts, retries/status handling, redirect and TLS rejection, malformed/oversized responses, pagination progress/cycles/page/item/aggregate-byte limits, declared read exceptions, rejection of the upstream RPC inventory, sequence IDs, connection limits, forced drain, malformed input, independent field-ID fixtures, metrics/probes/logging and service startup/shutdown. Local socket tests require execution outside the filesystem sandbox's network restriction.

No live UC, Trino Java-client, SELECT, Docker or Kubernetes integration was executed.
Milestone 1 is complete; subsequent milestones have not been started.
