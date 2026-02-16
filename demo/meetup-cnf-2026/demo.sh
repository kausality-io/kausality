#!/usr/bin/env bash
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "$0")" && pwd)"

# demo-magic setup
. "$DEMO_DIR/demo-magic.sh"
TYPE_SPEED=40
NO_WAIT=false
DEMO_PROMPT="${GREEN}➜ ${COLOR_RESET}"

clear

# ============================================================================
# Act 1 — "The Setup"
# ============================================================================

p "# Act 1: The Setup"
p "# Let's see kausality running in our cluster."
wait

pe "kubectl get pods -n kausality-system"
wait

p ""
p "# Create a Kausality policy — enforce mode for our Crossplane resources."
pe "cat $DEMO_DIR/manifests/kausality-policy.yaml"
wait
pe "kubectl apply -f $DEMO_DIR/manifests/kausality-policy.yaml"
wait

p ""
p "# Apply XRDs and Compositions for our GPU inference hierarchy."
pe "kubectl apply -f $DEMO_DIR/manifests/xgpucluster-xrd.yaml"
pe "kubectl apply -f $DEMO_DIR/manifests/xinferencecluster-xrd.yaml"
pe "kubectl apply -f $DEMO_DIR/manifests/xgpucluster-composition.yaml"
pe "kubectl apply -f $DEMO_DIR/manifests/xinferencecluster-composition.yaml"

p ""
p "# Wait for XRDs to be established..."
kubectl wait --for=condition=Established compositeresourcedefinition/xgpuclusters.test.kausality.io --timeout=60s
kubectl wait --for=condition=Established compositeresourcedefinition/xinferenceclusters.test.kausality.io --timeout=60s
p "# XRDs established."
wait

p ""
p "# Now create the XInferenceCluster — 8x B200 GPUs for our LLM training."
pe "cat $DEMO_DIR/manifests/xinferencecluster.yaml"
wait
pe "kubectl apply -f $DEMO_DIR/manifests/xinferencecluster.yaml"
wait

p ""
p "# Wait for the composition hierarchy to build..."
sleep 15

pe "kubectl get xinferencecluster,xgpucluster,nopresource"
wait

# Capture dynamic resource names
GPU_NAME=$(kubectl get xgpucluster -o jsonpath='{.items[?(@.metadata.ownerReferences[0].name=="llm-d-cluster")].metadata.name}')
NOP_NAME=$(kubectl get nopresource -o jsonpath='{.items[0].metadata.name}')

p ""
p "# Look at the kausality annotations on the NopResource (the leaf)."
pe "kubectl get nopresource $NOP_NAME -o jsonpath='{.metadata.annotations.kausality\\.io/trace}' | jq ."
wait
pe "kubectl get nopresource $NOP_NAME -o jsonpath='{.metadata.annotations.kausality\\.io/updaters}'"
p ""
wait

# ============================================================================
# Act 2 — "Reconciliation is Expected"
# ============================================================================

p "# Act 2: Reconciliation is Expected"
p "# The parent is stable — generation equals observedGeneration."
wait

pe "kubectl get xinferencecluster llm-d-cluster -o jsonpath='generation={.metadata.generation} observedGeneration={.status.conditions[?(@.type==\"Synced\")].observedGeneration}'"
p ""
wait

p ""
p "# Scale up the node pool — exactly like the GPU story. 8 → 16 replicas."
pe "kubectl patch xinferencecluster llm-d-cluster --type=merge -p '{\"spec\":{\"nodePools\":[{\"name\":\"training\",\"gpu\":\"b200\",\"replicas\":16}]}}'"
wait

p ""
p "# Parent generation bumped — reconciliation is in progress."
pe "kubectl get xinferencecluster llm-d-cluster -o jsonpath='generation={.metadata.generation} observedGeneration={.status.conditions[?(@.type==\"Synced\")].observedGeneration}'"
p ""
wait

p ""
p "# Wait for reconciliation to complete..."
sleep 15

pe "kubectl get xinferencecluster llm-d-cluster -o jsonpath='generation={.metadata.generation} observedGeneration={.status.conditions[?(@.type==\"Synced\")].observedGeneration}'"
p ""
p "# gen == obsGen again. Reconciliation complete. No drift."
wait

# ============================================================================
# Act 3 — "New Causal Origin"
# ============================================================================

p ""
p "# Act 3: New Causal Origin"
p "# A platform engineer directly scales the GPU cluster to 1000"
p "# — bypassing the parent XInferenceCluster."
wait

pe "kubectl patch xgpucluster $GPU_NAME --type=merge -p '{\"spec\":{\"replicas\":1000}}'"
p ""
p "# Patch succeeded — this is a NEW CAUSAL ORIGIN."
p "# Different actor, not the controller. Not drift."
wait

p ""
p "# The trace resets — fresh 1-hop origin."
pe "kubectl get xgpucluster $GPU_NAME -o jsonpath='{.metadata.annotations.kausality\\.io/trace}' | jq ."
wait

p ""
p "# The updaters annotation now has two hashes — Crossplane SA + kubectl user."
pe "kubectl get xgpucluster $GPU_NAME -o jsonpath='{.metadata.annotations.kausality\\.io/updaters}'"
p ""
wait

p ""
p "# XInferenceCluster still says 16 replicas. But the engineer set 1000."
p "# The declared state and actual state have diverged."
pe "kubectl get xinferencecluster llm-d-cluster -o jsonpath='declared={.spec.nodePools[0].replicas}'"
p ""
pe "kubectl get xgpucluster $GPU_NAME -o jsonpath='actual={.spec.replicas}'"
p ""
wait

# ============================================================================
# Act 4 — "Drift Detection" (climax)
# ============================================================================

p ""
p "# Act 4: Drift Detection"
p "# Crossplane sees the mismatch. It tries to reset replicas to 16."
p "# But XInferenceCluster hasn't changed — gen equals obsGen."
p "# Nobody asked for this. That's DRIFT."
wait

p ""
p "# Waiting for Crossplane reconciliation attempt..."
sleep 15

pe "kubectl get xgpucluster $GPU_NAME -o jsonpath='replicas={.spec.replicas}'"
p ""
p "# Still 1000! The drift correction was BLOCKED."
wait

p ""
p "# Check the webhook logs:"
pe "kubectl logs -n kausality-system -l app.kubernetes.io/name=kausality-webhook --tail=50 | grep -i drift"
wait

p ""
p "# DRIFT DETECTED — Crossplane's revert was blocked."
p "# In the Terraform story, the GPUs were gone."
p "# With kausality, they're protected."
wait

# ============================================================================
# Act 5 — "Lifecycle: Deletion"
# ============================================================================

p ""
p "# Act 5: Cleanup"
p "# Delete the root resource. Kausality allows all controller activity"
p "# during the deletion phase."
wait

pe "kubectl delete xinferencecluster llm-d-cluster"
wait

p ""
p "# Resources are cleaned up."
sleep 5
pe "kubectl get xinferencecluster,xgpucluster,nopresource 2>&1 || true"
wait

p ""
p "# That's kausality. Causal drift detection for Kubernetes."
p "# github.com/kausality-io/kausality"
