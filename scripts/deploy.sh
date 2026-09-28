#!/usr/bin/env bash
# Credentials, kubectl context, namespace and environment secrets are supplied externally.
set -euo pipefail
[[ $# -eq 2 ]] || { echo "Usage: $0 IMAGE_REFERENCE NAMESPACE" >&2; exit 2; }
image=$1
namespace=$2
[[ -n "$image" && "$image" != -* && "$image" != *[[:space:]]* ]] || { echo 'Invalid image reference' >&2; exit 2; }
[[ ${#namespace} -le 63 && "$namespace" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || { echo 'Invalid namespace' >&2; exit 2; }
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# Read only non-secret settings. Do not create/replace environment-specific resources.
kubectl get namespace "$namespace" -o name
for key in UC_BASE_URL UC_CATALOG; do
  value=$(kubectl -n "$namespace" get configmap uc-trino-metastore-environment -o "jsonpath={.data.$key}")
  [[ -n "$value" ]] || { echo "Environment ConfigMap is missing $key" >&2; exit 1; }
done
kubectl -n "$namespace" get secret uc-adapter-token -o name
rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT
# Render locally first; never briefly apply the placeholder image.
kubectl set image --local -f deploy/kubernetes/deployment.yaml "adapter=$image" -o yaml > "$rendered"
kubectl -n "$namespace" apply -f deploy/kubernetes/configmap.yaml \
  -f deploy/kubernetes/service.yaml -f deploy/kubernetes/networkpolicy.yaml \
  -f deploy/kubernetes/pdb.yaml -f "$rendered"
status=0
kubectl -n "$namespace" rollout status deployment/uc-trino-metastore-adapter --timeout="${ROLLOUT_TIMEOUT:-180s}" || status=$?
kubectl -n "$namespace" get pods -l app.kubernetes.io/name=uc-trino-metastore-adapter -o wide
kubectl -n "$namespace" get service uc-trino-metastore -o wide
exit "$status"
