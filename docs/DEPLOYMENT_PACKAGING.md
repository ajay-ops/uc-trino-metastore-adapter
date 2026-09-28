# Kubernetes POC packaging

Historical packaging verification. The current Git handoff and external build/deploy
procedure is [BUILD_AND_DEPLOY.md](BUILD_AND_DEPLOY.md); its scripts and environment
provisioning requirements supersede the manual procedure below.

Packaging is prepared; no ECR push or Kubernetes cluster deployment was performed.
Metadata translation, the five read RPCs and Delta behavior are unchanged. Native
Delta SELECT/N+1 acceptance remains deferred as recorded in MILESTONE_4.md.

## Artifacts and defaults

- Executable: `bin/uc-trino-metastore-adapter` (`make build`).
- Docker runtime: scratch, CA roots and one static binary, UID/GID 65532, SIGTERM.
- Thrift: `:9083`. HTTP: `:8080`, `/health/live`, `/health/ready`, `/metrics`, `/version`.
- Canonical env: UC_BASE_URL, UC_CATALOG, UC_TOKEN, THRIFT_ADDR, HTTP_ADDR, LOG_LEVEL, UC_CONNECT_TIMEOUT, UC_REQUEST_TIMEOUT.
- Legacy address/timeout variables and /livez,/readyz remain aliases. Canonical variables take precedence. UC_TOKEN_FILE remains a mutually exclusive alternative for file-based rotation. Environment-token rotation needs a process/pod restart.
- ClusterIP Service: `uc-trino-metastore`, named ports thrift:9083/http:8080. No public service or ingress. DNS in namespace `uc-poc`: `uc-trino-metastore.uc-poc.svc.cluster.local` (assuming default cluster suffix).
- POC: one replica; requests 100m CPU/64Mi, limits 1 CPU/512Mi. Production recommendation: >=2 replicas, suitable placement and minAvailable:1 PDB. POC PDB maxUnavailable:1 allows maintenance with downtime.
- Credentials use Secret `uc-adapter-token`, key UC_TOKEN; none are in ConfigMap or image. The empty secret-example is documentation, not a deployable credential.
- Probes use the new health paths. Readiness is local initialization, not live UC/Delta acceptance. Existing 15s bounded graceful shutdown is retained under Kubernetes's 30s grace period.

## Created files

```text
deploy/kubernetes/secret-example.yaml
docs/DEPLOYMENT_PACKAGING.md
```

## Modified files

```text
.dockerignore
Dockerfile
Makefile
README.md
cmd/server/main_test.go
internal/config/config.go
internal/config/config_test.go
internal/unity/client.go
internal/unity/client_test.go
internal/observability/observability.go
internal/observability/observability_test.go
deploy/kubernetes/README.md
deploy/kubernetes/deployment.yaml
deploy/kubernetes/service.yaml
deploy/kubernetes/configmap.yaml
deploy/kubernetes/networkpolicy.yaml
deploy/kubernetes/pdb.yaml
```

The repository was already untracked; this inventory records changes for this task,
not a diff against a committed baseline. The Go module/dependencies, translation,
IDL/generated files and metadata handlers were not modified.

## Verification

Passed locally:

- `go test ./...`: all packages passed, including metadata golden/TCP contracts.
- `go test -race ./...`: all packages passed.
- `make build vet`: named static executable built; vet passed.
- `gofmt -l cmd internal integration`: no output.
- Strict YAML parsing of all six Kubernetes manifests: passed. This is syntax verification, not Kubernetes API/CNI validation.
- `docker --context colima-uc-adapter-test build --build-arg VERSION=poc --build-arg REVISION=packaging-test -t uc-trino-metastore-adapter:poc .`: passed; image ID prefix `600221f4b976`.
- Temporary container smoke test with dummy credentials and localhost-only ports: non-root/read-only runtime, /health/live, /health/ready, /metrics, /version and TCP 9083 passed. Version returned service name, version=poc, revision=packaging-test and Go 1.27.1.
- SIGTERM drain/exit: exit code 0, approximately 0.147s; startup/drain/stop logs present and dummy token absent from logs. The temporary container was removed afterward.

No real UC request, live Trino/Delta query, ECR push or Kubernetes rollout was
executed. Actual namespace labels, image pull permissions, UC credentials/CA,
resource sizing and cluster network enforcement must be validated during deployment.
The scratch image has no shell; use HTTP probes/metrics/logs for diagnosis.

## Deployment and test sequence

[README](../README.md) contains complete local build/run, Docker build/run,
ECR login/tag/push and kubectl instructions. In order:

1. Run tests; build for the cluster architecture and push the image to ECR.
2. Set its registry digest in Deployment; set UC URL/catalog in ConfigMap.
3. Create namespace and real UC Secret from a credential file or secret manager. Do not apply the empty Secret example over real credentials.
4. Adjust NetworkPolicy to actual namespaces/labels; apply ConfigMap, Service, policy, PDB and Deployment explicitly. Do not apply the entire directory blindly.
5. Wait for rollout; inspect logs and /health/ready, /version and /metrics via local port-forward.
6. Configure official Trino Delta catalog, retaining governance/storage settings, then test metadata queries. Native Delta live POC is a separate gate.

Required Trino catalog core (same namespace):

```properties
connector.name=delta_lake
hive.metastore.uri=thrift://uc-trino-metastore:9083
```

The README also includes impersonation=false, metadata write-back=false and
read-only connector configuration. Cross-namespace clients use the full Service
DNS name and need a matching NetworkPolicy namespace selector. No new Delta
functionality or optimization is included in this packaging task.
