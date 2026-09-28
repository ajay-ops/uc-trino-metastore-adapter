# Git-to-build-system deployment workflow

```text
Development system -> Git commit/tag -> separate build/deployment system
                    -> verified container image -> ECR -> Kubernetes -> smoke tests
```

The development/Codex system edits and verifies source only. The separate system
owns the final container build, registry push, cluster credentials and deployment.
Nothing in the scripts discovers credentials, embeds tokens or selects a cluster.
Milestone 3 metadata behavior is unchanged; native Delta SELECT certification is
still deferred.

## Source handoff

Commit the source, go.mod/go.sum, generated Thrift Go/IDL, upstream license/IDL,
golden fixtures, scripts, Dockerfile and Kubernetes templates. No binary is needed.
A source commit and remote push are prerequisites for the build-system handoff.
Review `git status --short` and secrets before publishing.
Generated Go is already present; compilation does not need the Thrift compiler.

On the separate build system:

```sh
git clone "$SOURCE_REPOSITORY_URL" uc-trino-metastore-adapter
cd uc-trino-metastore-adapter
git fetch --tags origin
git checkout --detach refs/tags/v0.3.0
# Compare this SHA with the externally approved release SHA, not merely a mutable tag.
git rev-parse HEAD
git status --porcelain
./scripts/verify.sh
```

Tags/versions/registry placeholders in this guide are examples. Use the approved
exact commit; a signed/verified release tag is recommended when your process uses
signatures. `build-image.sh` rejects dirty tracked or untracked source, so the
reported Git SHA corresponds to the source checkout. Ignored local binaries are
excluded from the Docker context and are never build inputs.

## Tooling and Go dependencies

Verification needs Go 1.27.1, Bash, Python 3, Git and basic Unix utilities.
Go's toolchain auto-download is disabled (`GOTOOLCHAIN=local`). Online builds need
module downloads from the configured GOPROXY (default public Go proxy) and checksum
verification via GOSUMDB, plus the pinned builder image from Docker Hub or an
approved mirror. go.mod/go.sum pin module versions/checksums; `-mod=readonly`
prevents unnoticed dependency edits. Tests bind local loopback HTTP/Thrift ports.

```sh
go mod download
./scripts/verify.sh
make build VERSION=v0.3.0 REVISION="$(git rev-parse HEAD)" \
  BUILD_TIMESTAMP="$(git show -s --format=%cI HEAD)"
# bin/uc-trino-metastore-adapter; this local binary is not used by Docker.
```

An offline option is to prepare vendor/ on a connected, approved source-preparation
system using the same Go version, then review and commit it as a new exact source
commit before transferring through Git:

```sh
go mod vendor
# Review and commit vendor/ together with go.mod/go.sum; build that exact commit.
GOPROXY=off GOSUMDB=off ./scripts/verify.sh
CGO_ENABLED=0 GOPROXY=off GOSUMDB=off go build -mod=vendor -buildvcs=false \
  -trimpath -o bin/uc-trino-metastore-adapter ./cmd/server
```

verify.sh and Dockerfile automatically select vendor mode when vendor/ is present.
Vendor is not included by default. Obtain/verify dependencies online first; do not
turn off checksum verification for ordinary network builds. The restricted builder
also needs Go/Python/Git/Bash and the pinned container base image preloaded or
mirrored. Vendor removes module downloads, not the need for the Go builder image.
An internal Go proxy can instead be configured in your build-system environment;
private proxy credentials must never be committed or passed as Docker build args.

## Reproducible image build — separate system only

Docker Engine/CLI with BuildKit is required for release builds. Pin the Docker/
BuildKit version and target platform in the build system too. The Dockerfile pins
the Go/base-image digest, disables VCS auto-stamping, uses trimpath and clears the
Go build ID. No local binaries, Git directory, environment files or credentials
are copied into the build context. The runtime is scratch with CA roots and the
static binary, UID/GID 65532.

```sh
export DOCKER_BUILDKIT=1
export PLATFORM=linux/amd64  # choose your cluster node architecture
./scripts/build-image.sh "$IMAGE_REPOSITORY" v0.3.0
# Example result: REGISTRY/uc-trino-metastore-adapter:v0.3.0
# Builds only; never pushes.
```

Equivalent explicit Docker command from a verified clean checkout:

```sh
docker build --platform "$PLATFORM" \
  --build-arg VERSION=v0.3.0 \
  --build-arg GIT_SHA="$(git rev-parse HEAD)" \
  --build-arg BUILD_TIMESTAMP="$(git show -s --format=%cI HEAD)" \
  --build-arg SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)" \
  -t "$IMAGE_REPOSITORY:v0.3.0" .
```

/version exposes `version`, `git_sha`, `build_timestamp`, service and Go version;
`revision` remains an alias for git_sha. OCI labels carry the same metadata.
Default build timestamp is the commit timestamp, so identical source/version/
platform/toolchain inputs do not get a new embedded wall clock on each rebuild.
CI may set BUILD_TIMESTAMP explicitly to an RFC3339 build time; record it, since
changing it intentionally changes the binary. SOURCE_DATE_EPOCH uses commit epoch.
Image manifest byte identity also depends on BuildKit/exporter/provenance settings;
no cross-builder byte-identical image claim has been tested here. Record the pushed
digest and deploy by digest rather than relying on mutable tags.

## ECR authentication and push

Only the separate system receives AWS identity/permissions. The ECR repository must
already exist (provision it in your infrastructure workflow). Follow
[Amazon ECR push guidance](https://docs.aws.amazon.com/AmazonECR/latest/userguide/docker-push-ecr-image.html).

```sh
export AWS_REGION=YOUR_REGION
export ECR_REGISTRY=YOUR_ACCOUNT.dkr.ecr.YOUR_REGION.amazonaws.com
export IMAGE_REPOSITORY="$ECR_REGISTRY/uc-trino-metastore-adapter"
./scripts/build-image.sh "$IMAGE_REPOSITORY" v0.3.0
aws ecr get-login-password --region "$AWS_REGION" | \
  docker login --username AWS --password-stdin "$ECR_REGISTRY"
docker push "$IMAGE_REPOSITORY:v0.3.0"
# Obtain and record the pushed digest using your registry/build system.
export IMAGE_REFERENCE="$IMAGE_REPOSITORY@sha256:ACTUAL_PUSHED_DIGEST"
```

## Environment provisioning and deployment

The deployment system supplies kubectl context/credentials externally, an existing
namespace, node image-pull access and environment-specific network policy labels.
Review the active context before invoking the script. No credentials live in it.
Templates have no namespace, ECR account, production endpoint or real token.

Defaults ConfigMap is managed by deploy.sh. Required environment ConfigMap
`uc-trino-metastore-environment` and Secret `uc-adapter-token` are managed separately;
the script checks their existence but never overwrites them. The environment
ConfigMap supplies UC_BASE_URL/UC_CATALOG (and may override non-secret defaults).
The Secret contains UC_TOKEN. Create/update these through your environment's
configuration/secret manager. For initial manual provisioning on that system:

```sh
export NAMESPACE=YOUR_POC_NAMESPACE
kubectl config current-context
kubectl create namespace "$NAMESPACE"  # once, if not already provisioned
kubectl -n "$NAMESPACE" create configmap uc-trino-metastore-environment \
  --from-literal=UC_BASE_URL="$UC_BASE_URL" --from-literal=UC_CATALOG="$UC_CATALOG"
kubectl -n "$NAMESPACE" create secret generic uc-adapter-token \
  --from-file=UC_TOKEN=/secure/path/to/uc-token
./scripts/deploy.sh "$IMAGE_REFERENCE" "$NAMESPACE"
```

Never apply the whole deploy/kubernetes directory: secret-example.yaml is an empty
example. deploy.sh renders the requested image locally before applying the
Deployment, applies explicit defaults/Service/NetworkPolicy/PDB manifests, waits
for rollout and shows pods/Service. ROLLOUT_TIMEOUT defaults to 180s. Failures return
nonzero; the script does not silently roll back. It does not select a context,
create a namespace or change secrets.

One replica is the POC default; >=2 and a minAvailable:1 PDB are recommended for
production. Match NetworkPolicy selectors to actual Trino/monitoring namespaces
before publishing the environment's manifest revision. No public exposure is added.

## Rollout validation and smoke tests

```sh
kubectl -n "$NAMESPACE" rollout status deployment/uc-trino-metastore-adapter --timeout=180s
kubectl -n "$NAMESPACE" get pods,svc,endpointslices
kubectl -n "$NAMESPACE" logs deployment/uc-trino-metastore-adapter
kubectl -n "$NAMESPACE" port-forward service/uc-trino-metastore 8080:8080
# Separate terminal while port-forward remains running:
curl --fail http://127.0.0.1:8080/health/live
curl --fail http://127.0.0.1:8080/health/ready
curl --fail http://127.0.0.1:8080/version
curl --fail http://127.0.0.1:8080/metrics
```

Verify /version against the approved commit/version/timestamp. Check the deployed
image digest as well; a version string alone is not image attestation. Readiness
is local initialization, not proof of UC credentials or Delta query success.
After rotating environment-token Secrets or changing environment ConfigMaps, run
`kubectl -n "$NAMESPACE" rollout restart deployment/uc-trino-metastore-adapter`
and recheck rollout/probes. Existing pod environments do not refresh automatically.

Trino's same-namespace `uc_delta.properties`:

```properties
connector.name=delta_lake
hive.metastore.uri=thrift://uc-trino-metastore:9083
hive.metastore.thrift.impersonation.enabled=false
delta.metastore.store-table-metadata=false
delta.security=READ_ONLY
```

Keep existing governance and S3 identity settings. Across namespaces use
`uc-trino-metastore.<namespace>.svc.cluster.local:9083` (adapt cluster DNS suffix)
and permit that client namespace in NetworkPolicy. Test SHOW SCHEMAS, SHOW TABLES
and DESCRIBE against the selected UC table after reload. HMS-free SELECT and Spark
N+1 remain a separate live acceptance milestone, not implied by health checks.

## Rollback

Record the prior image digest, deployment revision and environment configuration
version before rollout. Prefer reapplying the previously approved Git/configuration
revision and exact prior image digest using deploy.sh. For Deployment-only rollback:

```sh
kubectl -n "$NAMESPACE" rollout history deployment/uc-trino-metastore-adapter
kubectl -n "$NAMESPACE" rollout undo deployment/uc-trino-metastore-adapter --to-revision=PREVIOUS_REVISION
kubectl -n "$NAMESPACE" rollout status deployment/uc-trino-metastore-adapter --timeout=180s
```

Deployment rollback does not restore ConfigMaps, Secrets, Service, policy or PDB.
Restore their approved environment versions separately if changed, and rerun health,
version and metadata checks. Preserve evidence of the failed rollout; do not change
Delta data or fall back silently to the old HMS.

## Handoff verification record

Local checks passed: verify.sh (go test ./..., go vet ./..., module checksums,
formatting, shell syntax and five mocked-CLI workflow tests), go test -race ./...,
and strict YAML parsing of all six Kubernetes manifests.

Before the initial project commit, verification created a temporary
source-only Git snapshot outside it, tagged and cloned that snapshot into a fresh
directory, and checked out the tag detached. The clone contained neither bin/ nor
vendor/. Verification and a static executable build passed without generated local
binaries; the source remained clean afterward. Go dependencies came from the
checksum-verified local module cache, not a newly tested public-network download.
A localhost smoke check verified /version against that temporary commit's version,
SHA and timestamp and confirmed clean SIGTERM exit.

Next, only in the temporary clone, go mod vendor prepared dependencies. With
GOPROXY=off and GOSUMDB=off, verification and an explicit -mod=vendor executable
build both passed. No vendor directory was added to the development repository.
The source's dependency/toolchain pins did not change.

The Docker and kubectl scripts were exercised with fake CLIs to verify argument
handling, dirty-checkout rejection, metadata injection, no push, image rendering
before apply, status reporting and rollout-failure propagation. No real Docker
build, registry push, Kubernetes API access or deployment was performed for this
handoff. Final image reproducibility and rollout smoke validation belong to the
separate build system; YAML parsing is not cluster validation.

Created files:

```text
scripts/verify.sh
scripts/build-image.sh
scripts/deploy.sh
scripts/test_workflow.py
docs/BUILD_AND_DEPLOY.md
```

Modified files:

```text
.dockerignore
.gitignore
.github/workflows/ci.yml
Dockerfile
Makefile
README.md
internal/observability/observability.go
internal/observability/observability_test.go
deploy/kubernetes/deployment.yaml
deploy/kubernetes/configmap.yaml
deploy/kubernetes/README.md
docs/DEPLOYMENT_PACKAGING.md
```

Use the approved source commit from the Git remote on the separate build system.
No release tag or deployment credential was created during handoff verification.
