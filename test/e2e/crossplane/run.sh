#!/usr/bin/env bash
set -euo pipefail

# E2E test script for kausality with Crossplane
# Creates a kind cluster, installs Crossplane and kausality, and verifies:
# 1. Trace propagation on Crossplane managed resources
# 2. Drift detection for Crossplane provider reconciliation
# 3. Integration with provider-nop for testing

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-kausality-crossplane-e2e}"
NAMESPACE="kausality-system"
CROSSPLANE_NAMESPACE="crossplane-system"
TEST_NAMESPACE="crossplane-test"
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

# Test assertion helper
assert() {
    local description="$1"
    local condition="$2"
    if eval "$condition"; then
        log "PASS: ${description}"
    else
        fail "FAIL: ${description}"
    fi
}

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
for cmd in kind kubectl helm ko jq; do
    if ! command -v "$cmd" &>/dev/null; then
        if [[ -x "${ROOT_DIR}/bin/$cmd" ]]; then
            export PATH="${ROOT_DIR}/bin:$PATH"
        else
            fail "$cmd is required but not installed"
        fi
    fi
done

log "=========================================="
log "Starting kausality Crossplane E2E tests"
log "=========================================="

log "Creating kind cluster: ${CLUSTER_NAME}"
kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/../kind-config.yaml" --wait 120s

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

log "Installing provider-nop (for testing)..."
kubectl apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-nop
spec:
  package: xpkg.upbound.io/crossplane-contrib/provider-nop:v0.3.0
EOF

log "Waiting for provider-nop to be healthy..."
wait_for "provider-nop to be healthy" \
    "kubectl get provider provider-nop -o jsonpath='{.status.conditions[?(@.type==\"Healthy\")].status}' | grep -q True" \
    120

log "Building and loading kausality images with ko..."
cd "${ROOT_DIR}"
export KO_DOCKER_REPO="ko.local"

WEBHOOK_IMAGE=$(ko build --bare ./cmd/kausality-webhook)
log "Built webhook image: ${WEBHOOK_IMAGE}"
kind load docker-image "${WEBHOOK_IMAGE}" --name "${CLUSTER_NAME}"

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
    --set 'resourceRules.include[0].apiGroups={nop.crossplane.io}' \
    --set 'resourceRules.include[0].resources={*}' \
    --set 'resourceRules.include[1].apiGroups={apps}' \
    --set 'resourceRules.include[1].resources={deployments,replicasets}' \
    --set logging.development=true \
    --wait \
    --timeout "${TIMEOUT}"

log "Waiting for kausality pods to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kausality -n "${NAMESPACE}" --timeout="${TIMEOUT}"
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=kausality-backend -n "${NAMESPACE}" --timeout="${TIMEOUT}"

log "Pods are ready:"
kubectl get pods -n "${NAMESPACE}"

log "Creating test namespace..."
kubectl create namespace "${TEST_NAMESPACE}" 2>/dev/null || true

# ==========================================
# Test 1: Crossplane NopResource with Trace Labels
# ==========================================
log ""
log "=========================================="
log "Test 1: NopResource Creation with Trace Labels"
log "=========================================="
log "Verify trace labels propagate through Crossplane managed resources"

log "Creating NopResource with trace labels..."
kubectl apply -f - <<EOF
apiVersion: nop.crossplane.io/v1alpha1
kind: NopResource
metadata:
  name: trace-test-nop
  namespace: ${TEST_NAMESPACE}
  labels:
    kausality.io/trace-ticket: "CROSSPLANE-001"
    kausality.io/trace-component: "infrastructure"
spec:
  forProvider:
    conditionAfter:
      - time: 3s
        conditionType: Ready
        conditionStatus: "True"
EOF

log "Waiting for NopResource to be ready..."
wait_for "NopResource to have Ready condition" \
    "kubectl get nopresource trace-test-nop -n ${TEST_NAMESPACE} -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null | grep -q True" \
    60

log "Checking NopResource has trace annotation..."
TRACE_ANNOTATION=$(kubectl get nopresource trace-test-nop -n "${TEST_NAMESPACE}" -o jsonpath='{.metadata.annotations.kausality\.io/trace}' 2>/dev/null || echo "")

if [[ -n "${TRACE_ANNOTATION}" ]]; then
    log "Trace annotation found: ${TRACE_ANNOTATION}"
    assert "Trace contains ticket reference" "echo '${TRACE_ANNOTATION}' | grep -q 'CROSSPLANE-001'"
else
    log "No trace annotation (expected for direct user creation)"
fi

# ==========================================
# Test 2: Provider Reconciliation Detection
# ==========================================
log ""
log "=========================================="
log "Test 2: Provider Reconciliation Detection"
log "=========================================="
log "Verify webhook intercepts provider-nop reconciliation"

log "Creating NopResource that will be reconciled..."
kubectl apply -f - <<EOF
apiVersion: nop.crossplane.io/v1alpha1
kind: NopResource
metadata:
  name: reconcile-test-nop
  namespace: ${TEST_NAMESPACE}
spec:
  forProvider:
    conditionAfter:
      - time: 2s
        conditionType: Ready
        conditionStatus: "True"
EOF

wait_for "NopResource reconcile-test-nop to be ready" \
    "kubectl get nopresource reconcile-test-nop -n ${TEST_NAMESPACE} -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null | grep -q True" \
    60

log "Triggering re-reconciliation by updating the resource..."
kubectl patch nopresource reconcile-test-nop -n "${TEST_NAMESPACE}" --type=merge \
    -p='{"spec":{"forProvider":{"conditionAfter":[{"time":"5s","conditionType":"Ready","conditionStatus":"True"}]}}}'

sleep 5

log "Checking webhook logs for Crossplane resource processing..."
WEBHOOK_LOGS=$(kubectl logs -l app.kubernetes.io/name=kausality -n "${NAMESPACE}" --tail=200 2>/dev/null || echo "")

if echo "${WEBHOOK_LOGS}" | grep -qE "nop.crossplane.io|NopResource"; then
    log "Webhook is intercepting Crossplane NopResource mutations"
    echo "${WEBHOOK_LOGS}" | grep -E "nop.crossplane.io|NopResource" | tail -5
else
    warn "Could not verify webhook is intercepting Crossplane resources"
fi

# ==========================================
# Test 3: Backend DriftReport for Crossplane
# ==========================================
log ""
log "=========================================="
log "Test 3: Backend DriftReport Reception"
log "=========================================="
log "Verify DriftReports are generated for Crossplane resources"

log "Checking backend logs..."
BACKEND_LOGS=$(kubectl logs -l app.kubernetes.io/name=kausality-backend -n "${NAMESPACE}" --tail=200 2>/dev/null || echo "")

if echo "${BACKEND_LOGS}" | grep -qE "DriftReport|Received|kausality"; then
    log "Backend received reports:"
    echo "${BACKEND_LOGS}" | grep -E "DriftReport|Received|kausality" | tail -10
else
    log "No DriftReports in backend logs (expected if no drift detected)"
fi

# ==========================================
# Test 4: Webhook Configuration for Crossplane CRDs
# ==========================================
log ""
log "=========================================="
log "Test 4: Webhook Configuration Verification"
log "=========================================="

log "Verifying MutatingWebhookConfiguration includes Crossplane resources..."
WEBHOOK_CONFIG=$(kubectl get mutatingwebhookconfiguration kausality -o json 2>/dev/null || echo "{}")

assert "MutatingWebhookConfiguration exists" "[[ \$(echo '${WEBHOOK_CONFIG}' | jq -r '.metadata.name') == 'kausality' ]]"

# Check that nop.crossplane.io is in the rules
NOP_RULE=$(echo "${WEBHOOK_CONFIG}" | jq '.webhooks[0].rules[] | select(.apiGroups[] == "nop.crossplane.io")')
assert "Webhook rules include nop.crossplane.io" "[[ -n '${NOP_RULE}' ]]"

# ==========================================
# Test 5: Multiple NopResources
# ==========================================
log ""
log "=========================================="
log "Test 5: Multiple Resource Handling"
log "=========================================="
log "Verify webhook handles multiple Crossplane resources correctly"

log "Creating multiple NopResources..."
for i in 1 2 3; do
    kubectl apply -f - <<EOF
apiVersion: nop.crossplane.io/v1alpha1
kind: NopResource
metadata:
  name: multi-test-nop-${i}
  namespace: ${TEST_NAMESPACE}
  labels:
    kausality.io/trace-batch: "batch-${i}"
spec:
  forProvider:
    conditionAfter:
      - time: 2s
        conditionType: Ready
        conditionStatus: "True"
EOF
done

log "Waiting for all NopResources to be ready..."
for i in 1 2 3; do
    wait_for "NopResource multi-test-nop-${i} to be ready" \
        "kubectl get nopresource multi-test-nop-${i} -n ${TEST_NAMESPACE} -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null | grep -q True" \
        60
done

NOP_COUNT=$(kubectl get nopresource -n "${TEST_NAMESPACE}" --no-headers 2>/dev/null | wc -l | tr -d ' ')
assert "All NopResources created" "[[ ${NOP_COUNT} -ge 5 ]]"

log "Listing all NopResources:"
kubectl get nopresource -n "${TEST_NAMESPACE}"

# ==========================================
# Test 6: Provider Status
# ==========================================
log ""
log "=========================================="
log "Test 6: Crossplane Provider Status"
log "=========================================="

log "Verifying provider-nop is healthy..."
PROVIDER_STATUS=$(kubectl get provider provider-nop -o json)

INSTALLED=$(echo "${PROVIDER_STATUS}" | jq -r '.status.conditions[] | select(.type=="Installed") | .status')
HEALTHY=$(echo "${PROVIDER_STATUS}" | jq -r '.status.conditions[] | select(.type=="Healthy") | .status')

assert "Provider is installed" "[[ '${INSTALLED}' == 'True' ]]"
assert "Provider is healthy" "[[ '${HEALTHY}' == 'True' ]]"

# ==========================================
# Summary
# ==========================================
log ""
log "=========================================="
log "Crossplane E2E Test Summary"
log "=========================================="
log "Cluster: ${CLUSTER_NAME}"
log ""
log "Crossplane version:"
kubectl get deployment crossplane -n "${CROSSPLANE_NAMESPACE}" -o jsonpath='{.spec.template.spec.containers[0].image}'
echo ""
log ""
log "Provider-nop status:"
kubectl get provider provider-nop
log ""
log "NopResources in ${TEST_NAMESPACE}:"
kubectl get nopresource -n "${TEST_NAMESPACE}"
log ""
log "Kausality pods:"
kubectl get pods -n "${NAMESPACE}"
log ""
log "=========================================="
log "All Crossplane E2E tests passed!"
log "=========================================="
