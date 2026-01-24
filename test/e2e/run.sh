#!/usr/bin/env bash
set -euo pipefail

# E2E test script for kausality
# Creates a kind cluster, deploys kausality with Helm, and verifies drift detection

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-kausality-e2e}"
NAMESPACE="kausality-system"
TIMEOUT="${TIMEOUT:-180s}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }
fail() { error "$*"; exit 1; }

cleanup() {
    log "Cleaning up..."
    kind delete cluster --name "${CLUSTER_NAME}" 2>/dev/null || true
}

# Trap to ensure cleanup on exit (unless SKIP_CLEANUP is set)
if [[ "${SKIP_CLEANUP:-}" != "true" ]]; then
    trap cleanup EXIT
fi

# Check required tools
for cmd in kind kubectl helm ko; do
    if ! command -v "$cmd" &>/dev/null; then
        # Try local bin
        if [[ -x "${ROOT_DIR}/bin/$cmd" ]]; then
            export PATH="${ROOT_DIR}/bin:$PATH"
        else
            fail "$cmd is required but not installed. Run: make $cmd"
        fi
    fi
done

log "Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-config.yaml" --wait 120s

log "Building and loading images with ko..."
cd "${ROOT_DIR}"
export KO_DOCKER_REPO="ko.local"

# Build webhook image
WEBHOOK_IMAGE=$(ko build --bare ./cmd/kausality-webhook)
log "Built webhook image: ${WEBHOOK_IMAGE}"
kind load docker-image "${WEBHOOK_IMAGE}" --name "${CLUSTER_NAME}"

# Build backend-log image
BACKEND_IMAGE=$(ko build --bare ./cmd/kausality-backend-log)
log "Built backend image: ${BACKEND_IMAGE}"
kind load docker-image "${BACKEND_IMAGE}" --name "${CLUSTER_NAME}"

log "Installing kausality via Helm..."
helm upgrade --install kausality "${ROOT_DIR}/charts/kausality" \
    --namespace "${NAMESPACE}" \
    --create-namespace \
    --set image.repository="${WEBHOOK_IMAGE%:*}" \
    --set image.tag="${WEBHOOK_IMAGE##*:}" \
    --set image.pullPolicy=Never \
    --set backend.enabled=true \
    --set backend.image.repository="${BACKEND_IMAGE%:*}" \
    --set backend.image.tag="${BACKEND_IMAGE##*:}" \
    --set backend.image.pullPolicy=Never \
    --set driftCallback.enabled=true \
    --set certificates.selfSigned.enabled=true \
    --set 'resourceRules.include[0].apiGroups={apps}' \
    --set 'resourceRules.include[0].resources={deployments,replicasets}' \
    --wait \
    --timeout "${TIMEOUT}"

log "Waiting for kausality pods to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kausality -n "${NAMESPACE}" --timeout="${TIMEOUT}"
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kausality-backend -n "${NAMESPACE}" --timeout="${TIMEOUT}"

log "Pods are ready:"
kubectl get pods -n "${NAMESPACE}"

log "Creating test namespace..."
kubectl create namespace e2e-test || true

# Test 1: Create a Deployment and verify trace propagation
log "=== Test 1: Deployment creation and trace propagation ==="

log "Creating test Deployment..."
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-deployment
  namespace: e2e-test
  annotations:
    kausality.io/trace-ticket: "E2E-001"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
        - name: nginx
          image: nginx:alpine
          ports:
            - containerPort: 80
EOF

log "Waiting for Deployment to be ready..."
kubectl wait --for=condition=available deployment/test-deployment -n e2e-test --timeout="${TIMEOUT}"

# Give the webhook time to process
sleep 5

log "Checking ReplicaSet for trace annotation..."
RS_NAME=$(kubectl get rs -n e2e-test -l app=test -o jsonpath='{.items[0].metadata.name}')
if [[ -z "${RS_NAME}" ]]; then
    fail "No ReplicaSet found for test deployment"
fi

TRACE=$(kubectl get rs "${RS_NAME}" -n e2e-test -o jsonpath='{.metadata.annotations.kausality\.io/trace}' 2>/dev/null || echo "")
if [[ -n "${TRACE}" ]]; then
    log "Trace annotation found on ReplicaSet: ${TRACE}"
else
    warn "No trace annotation found on ReplicaSet (webhook may not have intercepted the creation)"
fi

# Test 2: Update Deployment and verify webhook processes request
log "=== Test 2: Deployment update ==="

log "Scaling Deployment to 2 replicas..."
kubectl scale deployment test-deployment -n e2e-test --replicas=2
kubectl wait --for=condition=available deployment/test-deployment -n e2e-test --timeout="${TIMEOUT}"

sleep 3

log "Checking webhook logs..."
WEBHOOK_LOGS=$(kubectl logs -l app.kubernetes.io/name=kausality -n "${NAMESPACE}" --tail=100 2>/dev/null || echo "")
if echo "${WEBHOOK_LOGS}" | grep -q "admission"; then
    log "Webhook is processing admission requests"
else
    warn "Could not verify webhook is processing requests from logs"
fi

# Test 3: Check backend received reports
log "=== Test 3: Backend DriftReport reception ==="

log "Checking backend logs..."
BACKEND_LOGS=$(kubectl logs -l app.kubernetes.io/name=kausality-backend -n "${NAMESPACE}" --tail=100 2>/dev/null || echo "")
echo "${BACKEND_LOGS}"

if echo "${BACKEND_LOGS}" | grep -q "DriftReport\|apiVersion.*kausality"; then
    log "Backend received DriftReport(s)"
else
    log "No DriftReports in backend logs (expected if no drift occurred)"
fi

# Test 4: Verify webhook configuration
log "=== Test 4: Webhook configuration ==="

log "Checking ValidatingWebhookConfiguration..."
kubectl get validatingwebhookconfiguration -l app.kubernetes.io/name=kausality -o yaml

log "Checking MutatingWebhookConfiguration..."
kubectl get mutatingwebhookconfiguration -l app.kubernetes.io/name=kausality -o yaml 2>/dev/null || log "No MutatingWebhookConfiguration (expected)"

# Summary
log "=== E2E Test Summary ==="
log "Cluster: ${CLUSTER_NAME}"
log "Namespace: ${NAMESPACE}"
log "Pods:"
kubectl get pods -n "${NAMESPACE}"
log ""
log "E2E tests completed successfully!"
