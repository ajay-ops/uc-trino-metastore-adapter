# Kubernetes POC packaging

The deployment runs one `uc-trino-metastore-adapter` executable, with the five
approved read RPCs unchanged. Service `uc-trino-metastore` is ClusterIP only:
`thrift:9083` and `http:8080`. HTTP serves `/health/live`, `/health/ready`, `/metrics`
and `/version`. Readiness confirms local initialization, not UC availability or
successful Delta reads.

Deployment runs only on the separate build/deployment system. Follow
[BUILD_AND_DEPLOY.md](../../docs/BUILD_AND_DEPLOY.md). Manifests contain defaults
only; preprovision the namespace, ConfigMap `uc-trino-metastore-environment` with
UC_BASE_URL/UC_CATALOG, and Secret `uc-adapter-token` with UC_TOKEN. No actual
endpoint, cluster, registry account or credential is part of the templates.

```sh
./scripts/deploy.sh "$IMAGE_REFERENCE" "$NAMESPACE"
```

The script renders the exact image before applying, preserves externally managed
configuration/secrets, waits for rollout and shows status. Do not apply the whole
directory or empty Secret example. The Deployment image is a placeholder that the
script replaces locally. Update network selectors in the environment's reviewed
source/configuration revision before deployment.

Trino in the same namespace uses `thrift://uc-trino-metastore:9083`; across namespaces
use `thrift://uc-trino-metastore.uc-poc.svc.cluster.local:9083` (substitute your
namespace/cluster DNS suffix). HTTP is `http://uc-trino-metastore:8080` in namespace.
No LoadBalancer, NodePort or Ingress resource is used.

POC requests: 100m CPU/64Mi memory; limits: 1 CPU/512Mi. The pod runs as UID/GID
65532 with a read-only root filesystem, dropped capabilities, seccomp RuntimeDefault
and no mounted service-account token. SIGTERM drains within 15s; the pod grace is
30s. Rolling updates use maxUnavailable=0/maxSurge=1. Secret environment updates
require a rollout restart to take effect:

```sh
kubectl -n uc-poc rollout restart deployment/uc-trino-metastore-adapter
```

For production use **>=2 replicas**, appropriate placement, and change the PDB
from POC `maxUnavailable: 1` to `minAvailable: 1`. The POC PDB permits downtime
during voluntary maintenance of the sole replica. Resource defaults need workload
measurement, not an assumption of production capacity.

NetworkPolicy allows same-namespace pods labeled `app.kubernetes.io/name=trino`
on 9083 and `prometheus` on 8080. Match actual deployment labels/namespaces before
applying; cross-namespace clients require a deliberate namespace selector. This
requires an enforcing CNI and restricts ingress only. Kubelet probe behavior
must be checked in the actual cluster. Plain Thrift must stay cluster-internal.
For private UC CAs, mount the trusted bundle and configure `SSL_CERT_FILE`; do not
disable TLS verification. Optional `UC_TOKEN_FILE` remains supported for per-request
file rotation, but never set it together with UC_TOKEN.
