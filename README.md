# UC Trino Metastore Adapter

For teaching the end-to-end code and connectivity flow, use the
[short walkthrough](docs/CODE_WALKTHROUGH_SHORT.md) or the
[detailed walkthrough](docs/CODE_WALKTHROUGH_DETAILED.md).

A stateless Go service being built to bridge Trino's HMS Thrift metadata protocol
to OSS Unity Catalog. The official Trino Delta connector remains responsible for
Delta logs and Parquet. No Hive Metastore service or HMS database is required by
this skeleton.

**Milestone 3: schema and external Delta table discovery implemented.** The
five approved read RPCs are active: `get_all_databases`, `get_database`,
`get_table_meta`, `get_table_req` and `get_table`. Other RPCs, including writes,
return `TApplicationException(UNKNOWN_METHOD)`. Table locations come from UC;
Trino reads Delta schema from `_delta_log`. Live Trino SQL acceptance remains
unverified under the earlier integration deferral.

## Build/deployment ownership

Development prepares source only. The **separate build/deployment system** checks
out the approved exact Git commit, verifies it, builds/pushes to ECR, deploys and
runs smoke tests. See [BUILD_AND_DEPLOY.md](docs/BUILD_AND_DEPLOY.md) for the complete
clone/checkout, scripts, offline option, configuration provisioning and rollback flow.
The image and cluster commands below are for that separate system, not Codex.

## Local build and run

Use Go **1.27.1**. Generated bindings are checked in; no Thrift compiler is needed
for normal builds. Build one static executable:

```sh
go test ./...
make build VERSION=poc REVISION=unknown
# Output: bin/uc-trino-metastore-adapter
```

Set the UC token from your secret manager in the process environment (do not put
it in this repository or shell command arguments). Alternatively use
`UC_TOKEN_FILE=/absolute/path/to/uc-token`; set exactly one credential source.

```sh
export UC_BASE_URL=https://uc.example/api/2.1/unity-catalog
export UC_CATALOG=unity
export UC_TOKEN_FILE=/absolute/path/to/uc-token
export THRIFT_ADDR=:9083
export HTTP_ADDR=:8080
export LOG_LEVEL=info
export UC_CONNECT_TIMEOUT=1s
export UC_REQUEST_TIMEOUT=3s
./bin/uc-trino-metastore-adapter
```

HTTP on port 8080 provides `/health/live`, `/health/ready`, `/metrics`, and
`/version` (service/version/git_sha/build_timestamp/Go version JSON; revision alias retained). HMS Thrift listens on 9083.
Liveness is process-local; readiness validates local initialization and bound
listeners, not live UC availability or a successful Delta query. SIGTERM/SIGINT
marks the service unready, stops listeners, drains requests and forces remaining
sockets closed after `SHUTDOWN_TIMEOUT` (15s default). Kubernetes allows 30s.

```sh
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
curl --fail http://127.0.0.1:8080/version
curl --fail http://127.0.0.1:8080/metrics
```

For isolated local plaintext UC, explicitly set `UC_ALLOW_INSECURE_HTTP=true`.
TLS verification is never disabled; redirects are not followed. File tokens are
reread per HTTP attempt for rotation. `UC_TOKEN` is fixed at process startup;
restart pods after rotating the Kubernetes Secret. Credentials are never logged.

The UC client performs only schema/table GET/list calls with bounded pagination,
response sizes, retries and deadlines. Invalid identity, token cycles or page
failures abort the list; no partial success is returned. It has no mutation APIs.
`make check`, `make race` and `make check-generated` provide the other CI checks.
See [RPC status](docs/RPC_MATRIX.md) and [deployment packaging](docs/DEPLOYMENT_PACKAGING.md).

## Schema discovery

The adapter uses `GET /schemas?catalog_name=<UC_CATALOG>&max_results=100` and
follows every `next_page_token`, or `GET /schemas/<catalog>.<schema>` for an exact
lookup. It never calls an HMS service. Responses preserve schema name/case,
comment and `storage_location`; HMS parameters are an empty non-null map, and
owner, privileges and HMS catalog fields remain unset. It never derives a
location from `storage_root` or copies arbitrary UC properties.

Names must be single components: no dots, slashes, backslashes, ASCII spaces,
control characters, surrounding whitespace or HMS catalog markers (`@`, `#`, `!`).
Returned `catalog_name` must match configuration; `full_name`, when present,
must match catalog plus schema. Unsupported or inconsistent identities fail
rather than disappearing from discovery. No names are silently lowercased.

The target Trino 472 catalog file is:

```properties
connector.name=delta_lake
hive.metastore.uri=thrift://uc-trino-metastore:9083
hive.metastore.thrift.impersonation.enabled=false
delta.metastore.store-table-metadata=false
delta.security=READ_ONLY
```

Name it `uc_delta.properties`, leave `hive.metastore.thrift.catalog-name` unset,
and retain the existing deployment's governance configuration. The pending
runtime acceptance is `SHOW SCHEMAS FROM uc_delta;` with no real HMS reachable.
This example has not been exercised against Trino in the current implementation.

## Table discovery and lookup

`get_table_meta` uses UC `GET /tables` with configured catalog, exact schema and
`max_results=50`, following all continuation tokens. Its supported pattern is an
exact ordinary schema and table `*`; broader patterns fail explicitly. An empty
type list or a list containing `EXTERNAL_TABLE` includes eligible tables; other
type filters return an empty list only after a successful UC lookup.

Both `get_table_req` and `get_table` use the same UC lookup and translation.
`get_table_req` accepts Trino's capabilities, but rejects any explicit HMS catalog
name: leave the HMS catalog setting unset. `get_tables`, `get_tables_by_type` and
`get_fields` remain unsupported because the approved Trino 472 TCP Delta path
does not need them.

Only `EXTERNAL` + `DELTA` tables are exposed. Known managed/non-Delta/view objects
are excluded from lists and rejected on direct lookup; malformed/unknown metadata
fails rather than being silently hidden. Supported locations are `s3://` and
`s3a://` with a bucket authority, no credentials, port, query or fragment. The
original location is preserved in both `sd.location` and
`sd.serdeInfo.parameters["path"]`, including spaces/escapes/trailing slashes.
No S3 access or credential vending is performed by the adapter.

The table parameter `spark.sql.sources.provider` is exactly `DELTA`. HMS columns
and partition keys are explicit empty lists; UC columns/properties never override
the provider, path or Delta schema. `DESCRIBE` relies on Trino reading a supported
Delta log with its own storage credentials, not on `get_fields`.

The pending SQL acceptance checks are:

```sql
SHOW TABLES FROM uc_delta.raw;
DESCRIBE uc_delta.raw.events;
```

Unit, TCP contract and independent wire tests use the clearly labeled
[synthetic golden fixtures](integration/golden/README.md). No sanitized real HMS
response was available; real-HMS comparison and live SQL acceptance remain pending.

## Configuration

All settings come from environment variables. Explicit malformed/empty values
fail validation rather than silently falling back.

| Variable | Default | Purpose |
|---|---|---|
| `UC_BASE_URL` | Required | Full HTTPS API base ending in `/api/2.1/unity-catalog` |
| `UC_CATALOG` | Required | One UC catalog namespace component |
| `UC_TOKEN` | Required unless file supplied | Bearer token from environment/Secret, at most 16 KiB |
| `UC_TOKEN_FILE` | Optional alternative | Mounted bearer-token file; do not combine with UC_TOKEN |
| `UC_ALLOW_INSECURE_HTTP` | `false` | Explicit local-development opt-in |
| `THRIFT_ADDR` | `:9083` | Unframed binary TCP bind address; port 0 allowed for tests |
| `HTTP_ADDR` | `:8080` | Health/Prometheus bind address |
| `UC_CONNECT_TIMEOUT` | `1s` | TCP connect and TLS handshake budget |
| `UC_REQUEST_TIMEOUT` | `3s` | Full HTTP attempt timeout |
| `UC_OPERATION_TIMEOUT` | `10s` | Total GET or pagination budget across pages/retries |
| `RETRY_MAX_ATTEMPTS` | `2` | Includes first request; permitted range 1–5 |
| `RETRY_INITIAL_BACKOFF` | `100ms` | Initial jittered backoff |
| `RETRY_MAX_BACKOFF` | `1s` | Backoff cap; Retry-After may exceed this but never the remaining operation budget |
| `UC_MAX_RESPONSE_BYTES` | `4194304` | Per-response cap, at most 64 MiB |
| `UC_MAX_LIST_BYTES` | `16777216` | Aggregate JSON item/token byte budget, at most 64 MiB |
| `UC_MAX_PAGES` | `1000` | Pagination page cap; exceeding it is an error |
| `UC_MAX_ITEMS` | `100000` | Pagination aggregate item cap |
| `CACHE_TTL` | `0s` | Only zero accepted; cache deliberately not implemented |
| `CACHE_MAX_ENTRIES` | `1000` | Validated future cache bound; no cache allocation in this milestone |
| `LOG_LEVEL` | `info` | debug/info/warn/error |
| `THRIFT_SOCKET_TIMEOUT` | `10s` | Socket read/write timeout |
| `THRIFT_MAX_CONNECTIONS` | `128` | Concurrent accepted sockets; also bounds UC HTTP connections per host |
| `THRIFT_MAX_MESSAGE_BYTES` | `4194304` | Apache protocol size limit, at most 64 MiB |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful drain budget before forced close |

Timeout order must be connect ≤ request ≤ operation, all positive. Retry delays
must be positive and ordered, with maximum backoff ≤ operation timeout. Cache
and connection limits must be positive. No runtime cache, token exchange, end-user
impersonation, HMS TLS/SASL, or OpenTelemetry exporter is implemented yet.

## Thrift definitions

The source is **trinodb/hive-thrift tag 2**, exactly the artifact used by Trino 472,
not a claim that every API from the existing HMS 3.1.3 server is supported.
[Provenance and the optional skew-field projection](third_party/hive-thrift/README.md)
document the source checksum, version, compiler and Go limitation.

With Apache Thrift **0.24.0**, Python 3 and Go installed:

```sh
make generate
make check-generated
```

Never edit generated Go or the derived IDL by hand. The source IDL is unchanged;
the generator selects the five planned method signatures and transitive types.
Go socket tests assert schema/table responses, declared read errors, unknown-method errors and request
sequence IDs. Independent field-ID fixtures test optional-field skipping. These
are not substitutes for later successful Trino Java-client/metadata-conversion tests.

## Docker build and run

The multi-stage image has a scratch runtime, one statically linked binary, CA
certificates, UID/GID 65532 and no shell or embedded credentials. Build for your
cluster's node architecture (use `--platform=linux/amd64` or `linux/arm64` when
needed; the default matches the builder host).

```sh
./scripts/build-image.sh uc-trino-metastore-adapter poc

# UC_TOKEN is already exported from your secret manager.
docker run --rm --name uc-trino-metastore-adapter \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  -p 127.0.0.1:9083:9083 -p 127.0.0.1:8080:8080 \
  -e UC_BASE_URL -e UC_CATALOG -e UC_TOKEN \
  -e THRIFT_ADDR=:9083 -e HTTP_ADDR=:8080 -e LOG_LEVEL=info \
  -e UC_CONNECT_TIMEOUT=1s -e UC_REQUEST_TIMEOUT=3s \
  uc-trino-metastore-adapter:poc
```

Do not supply token values as Docker build arguments. A read-only token file
mount plus `UC_TOKEN_FILE` is also supported; make it readable by UID 65532.
Legacy `THRIFT_ADDRESS`, `HTTP_ADDRESS`, `HTTP_CONNECT_TIMEOUT` and
`HTTP_REQUEST_TIMEOUT` remain aliases; the new names take precedence, even if
empty/invalid. `/livez` and `/readyz` remain aliases. HTTP's new default is 8080.

## ECR push example

Use an existing ECR repository, or create it once with the command below.
Build/tag an image for the cluster architecture first. Authentication/push follow
[the ECR instructions](https://docs.aws.amazon.com/AmazonECR/latest/userguide/docker-push-ecr-image.html).

```sh
export AWS_REGION=us-east-1
export AWS_ACCOUNT_ID=123456789012
export ECR_REGISTRY="$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"
export IMAGE="$ECR_REGISTRY/uc-trino-metastore-adapter:poc"
# Only if the repository does not exist:
aws ecr create-repository --region "$AWS_REGION" \
  --repository-name uc-trino-metastore-adapter
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "$ECR_REGISTRY"
docker tag uc-trino-metastore-adapter:poc "$IMAGE"
docker push "$IMAGE"
```

## Kubernetes deployment and test sequence

The Service is **ClusterIP only**, named `uc-trino-metastore`, with `thrift:9083`
and `http:8080`. No LoadBalancer, NodePort or Ingress is created. POC replicas=1;
production recommendation is **at least 2** with suitable pod placement and a
PDB retaining at least one ready replica. POC requests are 100m CPU/64Mi memory;
limits are 1 CPU/512Mi memory. Measure and adjust these defaults in your environment.

1. Run tests, build/push the image, and pass its immutable registry digest to `scripts/deploy.sh`. Ensure cluster nodes can pull from ECR.
2. Create the externally managed `uc-trino-metastore-environment` ConfigMap with UC_BASE_URL/UC_CATALOG. Adjust NetworkPolicy selectors
   for the Trino and metrics workloads; defaults allow same-namespace Trino on
   9083 and Prometheus on 8080. Keep the service cluster-internal.
3. Create the namespace and real Secret separately. `secret-example.yaml` contains
   only an empty example; do **not** apply it over a real Secret or apply the whole
   directory blindly. The following uses a credential file without putting the
   value in command arguments, per [Kubernetes Secret guidance](https://kubernetes.io/docs/tasks/configmap-secret/managing-secret-using-kubectl/).

```sh
kubectl create namespace uc-poc
kubectl -n uc-poc create secret generic uc-adapter-token \
  --from-file=UC_TOKEN=/absolute/path/to/uc-token
kubectl -n uc-poc create configmap uc-trino-metastore-environment \
  --from-literal=UC_BASE_URL="$UC_BASE_URL" --from-literal=UC_CATALOG="$UC_CATALOG"
./scripts/deploy.sh "$IMAGE_REFERENCE" uc-poc
kubectl -n uc-poc rollout status deployment/uc-trino-metastore-adapter --timeout=180s
kubectl -n uc-poc get pods,svc,endpointslices
kubectl -n uc-poc logs deployment/uc-trino-metastore-adapter
kubectl -n uc-poc port-forward service/uc-trino-metastore 8080:8080
# From another terminal:
curl --fail http://127.0.0.1:8080/health/ready
curl --fail http://127.0.0.1:8080/version
curl --fail http://127.0.0.1:8080/metrics
```

4. Configure/reload the Trino catalog, then test SHOW SCHEMAS, SHOW TABLES and
   DESCRIBE. Probe success does not prove UC authentication, native Delta reads,
   or the deferred HMS-free SELECT/N+1 acceptance.

Save as `uc_delta.properties` when Trino is in the same namespace:

```properties
connector.name=delta_lake
hive.metastore.uri=thrift://uc-trino-metastore:9083
hive.metastore.thrift.impersonation.enabled=false
delta.metastore.store-table-metadata=false
delta.security=READ_ONLY
```

Preserve your existing governance and Trino S3 configuration. Leave
`hive.metastore.thrift.catalog-name` unset. Across namespaces use
`thrift://uc-trino-metastore.uc-poc.svc.cluster.local:9083` and explicitly allow the
Trino namespace in the NetworkPolicy. HTTP DNS is
`http://uc-trino-metastore.uc-poc.svc.cluster.local:8080`. The cluster domain may
be customized; substitute its configured suffix when necessary.

The POC PDB allows one unavailable pod so the single replica does not block
voluntary node maintenance; that permits downtime. For production replicas >=2,
replace `maxUnavailable: 1` with `minAvailable: 1`. Plain Thrift is confined to the
cluster; configure validated TLS/mTLS termination before any broader use. No
cluster deployment or live Delta query is implied by these packaging instructions.

## Next steps

The approved [implementation plan](docs/IMPLEMENTATION_PLAN.md),
[RPC matrix](docs/RPC_MATRIX.md) and [field mapping](docs/UC_HMS_MAPPING.md) describe
subsequent work. `internal/translate` contains schema and external Delta metadata mapping;
`internal/cache` remains a package comment only. Do not infer readiness for Trino workloads from healthy infrastructure probes.
