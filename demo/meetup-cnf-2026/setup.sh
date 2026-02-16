#!/usr/bin/env bash
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== Kausality Demo Setup ==="
echo ""

# Step 1: Verify cluster access
echo "Checking cluster access..."
kubectl cluster-info --request-timeout=5s > /dev/null 2>&1 || {
    echo "ERROR: Cannot reach Kubernetes cluster. Is your kubeconfig set?"
    exit 1
}
echo "  Cluster reachable."

# Step 2: Verify kausality is running
echo "Checking kausality..."
kubectl rollout status deployment/kausality-webhook -n kausality-system --timeout=5s > /dev/null 2>&1 || {
    echo "ERROR: Kausality webhook not running. Run 'tilt up' or 'make install'."
    exit 1
}
echo "  Kausality webhook running."

# Step 3: Verify Crossplane is running
echo "Checking Crossplane..."
kubectl rollout status deployment/crossplane -n crossplane-system --timeout=5s > /dev/null 2>&1 || {
    echo "ERROR: Crossplane not running. Run 'make install-crossplane'."
    exit 1
}
echo "  Crossplane running."

# Step 4: Verify provider-nop is healthy
echo "Checking provider-nop..."
kubectl get provider provider-nop -o jsonpath='{.status.conditions[?(@.type=="Healthy")].status}' 2>/dev/null | grep -q True || {
    echo "ERROR: provider-nop not healthy. Run 'make install-crossplane'."
    exit 1
}
echo "  provider-nop healthy."

# Step 5: Verify function-patch-and-transform
echo "Checking function-patch-and-transform..."
kubectl get function function-patch-and-transform > /dev/null 2>&1 || {
    echo "ERROR: function-patch-and-transform not installed. Run 'make install-crossplane'."
    exit 1
}
echo "  function-patch-and-transform installed."

# Step 6: Clean up leftover resources from previous runs
echo ""
echo "Cleaning up leftover resources..."
kubectl delete xinferencecluster llm-d-cluster 2>/dev/null && echo "  Deleted leftover XInferenceCluster." || true
sleep 3
kubectl delete composition xinferencecluster-composition xgpucluster-composition 2>/dev/null && echo "  Deleted leftover Compositions." || true
kubectl delete compositeresourcedefinition xinferenceclusters.test.kausality.io xgpuclusters.test.kausality.io 2>/dev/null && echo "  Deleted leftover XRDs." || true
kubectl delete kausality crossplane-demo 2>/dev/null && echo "  Deleted leftover Kausality policy." || true

# Clean any orphaned NopResources from demo
for nop in $(kubectl get nopresource -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    kubectl delete nopresource "$nop" 2>/dev/null && echo "  Deleted leftover NopResource $nop." || true
done

sleep 3

# Step 7: Pre-warm XRDs (apply, wait for Established, then delete)
echo ""
echo "Pre-warming XRDs (avoids 60s wait during demo)..."
kubectl apply -f "$DEMO_DIR/manifests/xgpucluster-xrd.yaml"
kubectl apply -f "$DEMO_DIR/manifests/xinferencecluster-xrd.yaml"

echo "  Waiting for XRDs to be established..."
kubectl wait --for=condition=Established compositeresourcedefinition/xgpuclusters.test.kausality.io --timeout=60s
kubectl wait --for=condition=Established compositeresourcedefinition/xinferenceclusters.test.kausality.io --timeout=60s
echo "  XRDs established."

echo "  Deleting XRDs (demo will re-apply them)..."
kubectl delete compositeresourcedefinition xinferenceclusters.test.kausality.io xgpuclusters.test.kausality.io
sleep 3

# Step 8: Verify demo-magic.sh exists
echo ""
if [ ! -f "$DEMO_DIR/demo-magic.sh" ]; then
    echo "Downloading demo-magic.sh..."
    curl -sL https://raw.githubusercontent.com/paxtonhare/demo-magic/master/demo-magic.sh -o "$DEMO_DIR/demo-magic.sh"
    echo "  Downloaded."
else
    echo "demo-magic.sh already present."
fi

# Step 9: Verify jq is installed
echo ""
command -v jq > /dev/null 2>&1 || {
    echo "ERROR: jq not installed. Run 'brew install jq'."
    exit 1
}
echo "jq installed."

echo ""
echo "=== Setup complete. Ready for demo. ==="
echo "Run: bash $DEMO_DIR/demo.sh"
