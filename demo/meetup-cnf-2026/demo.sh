#!/usr/bin/env bash
set -euo pipefail

DEMO_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DEMO_DIR"

# demo-magic setup
. ./demo-magic.sh
TYPE_SPEED=40
NO_WAIT=false

# Abbreviated cwd for prompt (~/Q/k/d/meetup-cnf-2026 style).
_abbrev_path() {
    local cwd="${PWD/#$HOME/\~}"
    local IFS='/'
    local -a parts=($cwd)
    local result="" last=$((${#parts[@]} - 1))
    for i in "${!parts[@]}"; do
        if [[ $i -eq $last ]] || [[ $i -eq 0 ]]; then
            result+="${parts[$i]}"
        else
            result+="${parts[$i]:0:1}"
        fi
        [[ $i -lt $last ]] && result+="/"
    done
    printf '%s' "$result"
}
_PROMPT_PATH="$(_abbrev_path)"
_LAST_RC=0

# Render prompt: grey path + green/red ❯ based on last command's exit code.
_prompt() {
    local arrow
    if [[ $_LAST_RC -eq 0 ]]; then arrow="$GREEN"; else arrow="$RED"; fi
    printf "${GREY}${_PROMPT_PATH} ${arrow}❯${COLOR_RESET} "
}

# Keypress before typing: reprint prompt on Enter to stay on the right line.
# No cursor save/restore — relative movement handles terminal scroll correctly.
_keypress() {
    IFS= read -rn1 key </dev/tty
    if [[ -z "$key" ]]; then
        # Enter: cursor went down. Go back up, clear line, reprint prompt.
        printf '\033[A\r\033[K'
        _prompt
    else
        # Any other key: clear line and reprint prompt (erase echoed char).
        printf '\r\033[K'
        _prompt
    fi
}

# Override wait: show prompt, clean up on keypress.
# \033[A is relative (works even when terminal scrolls on Enter).
# \033[J clears the prompt line AND the blank line from Enter's echo.
function wait() {
    if [[ "$PROMPT_TIMEOUT" == "0" ]]; then
        _prompt
        IFS= read -rn1 key </dev/tty
        if [[ -z "$key" ]]; then
            printf '\033[A'    # Enter: go back up to prompt line
        fi
        printf '\r\033[J'     # clear from here to end of screen
    else
        read -rt "$PROMPT_TIMEOUT" </dev/tty
    fi
}

# Override run_cmd: track exit code for prompt color.
run_cmd() {
    trap '' SIGINT
    stty -echoctl
    eval "$@"
    _LAST_RC=$?
    stty echoctl
    trap - SIGINT
}

# Type text with pv animation.
# igncr during pv silently drops Enter (no echo, no newline, no input).
# Redraw after pv fixes any other stray chars that were echoed.
_type_text() {
    if [[ -z $TYPE_SPEED ]]; then
        echo -en "$1"
        return
    fi
    stty igncr </dev/tty
    echo -en "$1" | pv -qL $[$TYPE_SPEED+(-2 + RANDOM%5)]
    stty -igncr </dev/tty

    # Redraw the line to fix any stray echoed chars from during pv.
    printf '\r\033[K'
    _prompt
    echo -en "$1"
}

# Shared helper: render prompt, wait for keypress, type text. No newline.
_type_cmd() {
    if [[ ${1:0:1} == "#" ]]; then
        cmd=$DEMO_COMMENT_COLOR$1$COLOR_RESET
    else
        cmd=$DEMO_CMD_COLOR$1$COLOR_RESET
    fi

    _prompt

    if [[ "$NO_WAIT" == "false" ]]; then
        _keypress
    fi

    _type_text "$cmd"
}

# Override p: comments/narration type out immediately with no keypress.
function p() {
    if [[ -z "$1" ]]; then
        echo ""
        return
    fi

    if [[ ${1:0:1} == "#" ]]; then
        cmd=$DEMO_COMMENT_COLOR$1$COLOR_RESET
    else
        cmd=$DEMO_CMD_COLOR$1$COLOR_RESET
    fi

    _prompt
    _type_text "$cmd"
    echo ""
}

# Override pe: 2 keypresses — one to start typing, one to "execute".
function pe() {
    _type_cmd "$@"
    # Second keypress: "press Enter to execute".
    if [[ "$NO_WAIT" == "false" ]]; then
        IFS= read -rn1 key </dev/tty
        if [[ -z "$key" ]]; then
            :  # Enter: newline already echoed — serves as execution newline
        else
            printf '\b \b\n'  # erase echoed char, then newline
        fi
    else
        echo ""
    fi
    run_cmd "$@"
}

clear

# ============================================================================
# Act 1 — "The Setup"
# ============================================================================

p "# Act 1: The Setup"
p "# Let's see kausality running in our cluster."

pe "kubectl get pods -n kausality-system"
wait

p ""
p "# Create a Kausality policy — enforce mode for our Crossplane resources."
pe "cat manifests/kausality-policy.yaml"
wait
pe "kubectl apply -f manifests/kausality-policy.yaml"
wait

p ""
p "# Apply XRDs and Compositions for our GPU inference hierarchy."
pe "kubectl apply -f manifests/xgpucluster-xrd.yaml"
pe "kubectl apply -f manifests/xinferencecluster-xrd.yaml"
pe "kubectl apply -f manifests/xgpucluster-composition.yaml"
pe "kubectl apply -f manifests/xinferencecluster-composition.yaml"

p ""
pe "kubectl wait --for=condition=Established xrd/xgpuclusters.test.kausality.io xrd/xinferenceclusters.test.kausality.io --timeout=60s"

p ""
p "# Now create the XInferenceCluster — 8x B200 GPUs for our LLM training."
pe "cat manifests/xinferencecluster.yaml"
wait
pe "kubectl apply -f manifests/xinferencecluster.yaml"
wait

p ""
pe "kubectl wait --for=condition=Ready xinferencecluster/llm-d-cluster --timeout=60s"

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
pe "kubectl wait --for=condition=Synced xinferencecluster/llm-d-cluster --timeout=30s"

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
pe "sleep 15  # give Crossplane time to attempt reconciliation"

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

pe "kubectl delete xinferencecluster llm-d-cluster"
wait

p ""
pe "sleep 5  # wait for cascade delete"
pe "kubectl get xinferencecluster,xgpucluster,nopresource 2>&1 || true"
wait

p ""
p "# That's kausality. Causal drift detection for Kubernetes."
p "# github.com/kausality-io/kausality"
