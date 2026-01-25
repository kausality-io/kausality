#!/usr/bin/env bash
set -euo pipefail

# E2E test infrastructure setup for kausality with Crossplane
# This script only sets up the environment - all tests are in Go.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-kausality-crossplane-e2e}"
NAMESPACE="kausality-system"
CROSSPLANE_NAMESPACE="crossplane-system"
TIMEOUT="${TIMEOUT:-300s}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() { echo -e "${GREEN}[INFO]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }
fail() { error "$*"; exit 1; }

# Wait for a condition with timeout
wait_for() {
    local description="$1"
    local condition="$2"
    local timeout="${3:-60}"
    local interval="${4:-2}"

    log "Waiting for: ${description}"
    local elapsed=0
    while ! eval "$condition" 2>/dev/null; do
        if [[ $elapsed -ge $timeout ]]; then
            fail "Timeout waiting for: ${description}"
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log "Done: ${description}"
}

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
        if [[ -x "${ROOT_DIR}/bin/$cmd" ]]; then
            export PATH="${ROOT_DIR}/bin:$PATH"
        else
            fail "$cmd is required but not installed"
        fi
    fi
done

log "=========================================="
log "Setting up Crossplane E2E test environment"
log "=========================================="

# Create kind cluster
log "Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/../kind-config.yaml" --wait 120s

# Install Crossplane
log "Installing Crossplane..."
helm repo add crossplane-stable https://charts.crossplane.io/stable 2>/dev/null || true
helm repo update
helm upgrade --install crossplane crossplane-stable/crossplane \
    --namespace "${CROSSPLANE_NAMESPACE}" \
    --create-namespace \
    --wait \
    --timeout "${TIMEOUT}"

log "Waiting for Crossplane to be ready..."
kubectl wait --for=condition=ready pod -l app=crossplane -n "${CROSSPLANE_NAMESPACE}" --timeout="${TIMEOUT}"

# Install provider-nop
log "Installing provider-nop..."
kubectl apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-nop
spec:
  package: xpkg.upbound.io/crossplane-contrib/provider-nop:v0.3.0
EOF

wait_for "provider-nop to be healthy" \
    "kubectl get provider provider-nop -o jsonpath='{.status.conditions[?(@.type==\"Healthy\")].status}' | grep -q True" \
    120

# Install function-patch-and-transform
log "Installing function-patch-and-transform..."
kubectl apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1beta1
kind: Function
metadata:
  name: function-patch-and-transform
spec:
  package: xpkg.upbound.io/crossplane-contrib/function-patch-and-transform:v0.7.0
EOF

wait_for "function-patch-and-transform to be healthy" \
    "kubectl get function function-patch-and-transform -o jsonpath='{.status.conditions[?(@.type==\"Healthy\")].status}' | grep -q True" \
    120

# Build and load kausality images
log "Building and loading kausality images with ko..."
cd "${ROOT_DIR}"
export KO_DOCKER_REPO="ko.local"

WEBHOOK_IMAGE=$(ko build --bare ./cmd/kausality-webhook)
log "Built webhook image: ${WEBHOOK_IMAGE}"
kind load docker-image "${WEBHOOK_IMAGE}" --name "${CLUSTER_NAME}"

BACKEND_IMAGE=$(ko build --bare ./cmd/kausality-backend-log)
log "Built backend image: ${BACKEND_IMAGE}"
kind load docker-image "${BACKEND_IMAGE}" --name "${CLUSTER_NAME}"

# Install kausality
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
    --set 'resourceRules.include[0].apiGroups={nop.crossplane.io}' \
    --set 'resourceRules.include[0].resources={*}' \
    --set 'resourceRules.include[1].apiGroups={apps}' \
    --set 'resourceRules.include[1].resources={deployments,replicasets}' \
    --set 'resourceRules.include[2].apiGroups={test.kausality.io}' \
    --set 'resourceRules.include[2].resources={*}' \
    --set driftDetection.defaultMode=enforce \
    --set logging.development=true \
    --wait \
    --timeout "${TIMEOUT}"

log "Waiting for kausality pods to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kausality -n "${NAMESPACE}" --timeout="${TIMEOUT}"
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kausality-backend -n "${NAMESPACE}" --timeout="${TIMEOUT}"

log "Environment ready:"
kubectl get pods -n "${NAMESPACE}"

# Run Go E2E tests
log ""
log "=========================================="
log "Running Go E2E Tests"
log "=========================================="

cd "${ROOT_DIR}"
go test -tags=e2e ./test/e2e/crossplane -v -timeout 10m

log ""
log "=========================================="
log "All Crossplane E2E tests passed!"
log "=========================================="
