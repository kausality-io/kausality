---
marp: true
theme: default
paginate: true
backgroundColor: #fff
style: |
  section {
    font-family: 'Helvetica Neue', Arial, sans-serif;
  }
  section.lead h1 {
    font-size: 2.5em;
  }
  section.lead p {
    font-size: 1.2em;
  }
  blockquote {
    border-left: 4px solid #2563eb;
    padding-left: 1em;
    color: #374151;
    font-style: italic;
  }
  table {
    font-size: 0.85em;
  }
  code {
    font-size: 0.85em;
  }
  pre {
    font-size: 0.75em;
  }
  section.small-code pre {
    font-size: 0.65em;
  }
---

<!-- _class: lead -->
<!-- _paginate: false -->

# Kausality

**Drift Detection for Kubernetes**

*"Every mutation needs a cause."*

CNCF Frankfurt Meetup 2026

---

## About Me

<!-- TODO: fill in speaker details -->

- **Name**: Dr. Stefan Schimanski
- **Role**: ...
- **GitHub**: github.com/sttts

---

## A Story About 1000 GPUs

We deployed **1000 B200 GPU nodes** in production.

Burn-in was done carefully -- nodes scaled up **manually in the AWS console**, slowly, deliberately. It worked.

A few days later, a controller update removed an unrelated AWS add-on.

That triggered **Terraform reconciliation**.

---

## What Happened?

Terraform's desired state said: **far fewer than 1000 nodes**.

Terraform did exactly what it was told.

**No bug. Hundreds of nodes gone.**

---

## Two Drifts Collided

**1. Human-caused drift**
Manual scaling in AWS -- never recorded in Terraform state.

**2. Software-caused drift**
Controller update removed an unrelated add-on (intended as no-op).
But it triggered Terraform reconciliation.

These drifts were **unrelated** but **connected by consequence**.

---

## The Root Cause

> Declarative systems converge to **declared state**, not **intended state**.

This isn't a Terraform problem. It applies to **Crossplane, Pulumi, ArgoCD** -- any declarative IaC tool running without a human in the loop.

> If your infrastructure can't explain **why** something exists, eventually it will delete it.

---

## Controller Hierarchies

Kubernetes has become **the universal control plane**. Everything is a controller tree:

```
Deployment                    Crossplane XR
  └── ReplicaSet                └── Composition
        └── Pod                       └── ManagedResource --> Cloud API
```

```
OpenTofu Workspace            ACK DBInstance
  └── terraform plan/apply      └── AWS RDS API call
        └── Cloud resources           └── RDS instance
```

```
Cluster API Cluster           ArgoCD Application
  └── MachineDeployment         └── kubectl apply
        └── Machine                   └── K8s resources
```

Different tools, **same pattern**: CRD --> controller --> children (K8s or cloud).

---

## The Universal Contract

Every controller -- native, Crossplane, ACK, OpenTofu -- follows the same loop:

1. **Watch** the parent CRD's spec
2. **Reconcile** children to match (K8s objects, cloud API calls, Terraform runs)
3. **Report** status back on the parent

The **generation/observedGeneration** contract:

- `metadata.generation` increments on every spec change
- `status.observedGeneration` is set by the controller after reconciling

When `gen == obsGen`: the controller has caught up. The system is **stable**.

This contract is the same whether children are Pods, S3 Buckets, or EKS clusters.

---

## What Can Change an Object?

Every Kubernetes CRD -- whether it represents a Pod or an AWS VPC -- can be mutated by different actors:

| Actor | Example |
|-------|---------|
| **Human** | `kubectl edit`, AWS console, `tofu apply` |
| **GitOps tool** | ArgoCD, Flux applying manifests |
| **Owning controller** | Crossplane, ACK, OpenTofu controller |
| **Non-owning controller** | HPA, Karpenter, KEDA |
| **External drift** | Cloud provider API, another team's Terraform |

The API server doesn't distinguish between these. A mutation is a mutation.

And for cloud resources: the **external world can change underneath** without Kubernetes knowing.

---

## Three Types of Changes

When a mutation happens, there are exactly **three situations**:

| # | Parent State | Meaning |
|---|-------------|---------|
| 1 | `gen != obsGen` | Controller is **reconciling** a spec change |
| 2 | `gen == obsGen` | Controller is acting **without a spec change** |
| 3 | Actor is not the controller | Someone else is making a change |

**#1** is expected. You changed the Crossplane XR, ACK resource, or OpenTofu Workspace -- the controller applies it.

**#2** is **drift**. Something triggered reconciliation, but not a spec change. The OpenTofu controller re-runs `tofu apply`. ACK re-syncs from AWS. Crossplane re-renders the composition.

**#3** is a **new causal origin**. A different actor, a different intent.

---

## Why Does Drift Happen?

Drift (#2) -- controller acts while parent is stable -- has real causes:

- **External state changed**: someone modified the S3 bucket via AWS console; ACK or Crossplane "corrects" it
- **Software update**: new controller version reconciles differently (our GPU story)
- **Transitive dependency**: a referenced ConfigMap or Terraform module changed
- **Composition change**: an unrelated field in a Crossplane composition changed, triggering re-render of all children
- **Periodic re-sync**: OpenTofu controller runs `tofu plan` on schedule, finds external drift, applies
- **Bug**: controller enters a reconcile loop

Every IaC controller does this. They "helpfully" converge reality to declared state -- **without anyone asking**.

---

## The Problem With Convergence

Declarative systems promise: **declare what you want, the system makes it so**.

The dark side: **the system cannot distinguish** between:

- State it should converge to (intentional)
- State that exists for a reason it doesn't know about (unintentional to destroy)

This is the **same problem** in every tool:

| Tool | What happens |
|------|-------------|
| **Terraform/OpenTofu** | `tofu apply` destroys manually scaled nodes |
| **Crossplane** | Composition re-render reverts cloud resource changes |
| **ACK** | Periodic sync overwrites console changes |
| **ArgoCD** | Self-heal reverts manual hotfix |

The controller doesn't know **why** reality looks different. It just converges.

---

## The Missing Concept: Causality

Kubernetes tracks **what** the desired state is.

It does not track **why** the current state looks the way it does.

To detect drift, we need to answer:

1. **Who** is making this change?
2. **Why** are they making it? Was a spec change requested?
3. **Is this expected?** Does the parent's state justify this mutation?

This is **causality** -- linking every mutation to its cause.

---

## Challenge 1: Who Is the Controller?

The API server tells us the **user** making a request:

```
system:serviceaccount:kube-system:deployment-controller
```

But how do we know this user is **the** controller for a given child?

- Controllers don't announce themselves
- `fieldManager` depends on client correctness
- Multiple service accounts might touch the same object

---

## Controller Identification

**Observation**: every controller -- Crossplane, ACK, OpenTofu, native -- does two things:

1. Updates **children's spec** (reconciliation)
2. Updates **parent's status** (reporting back)

If we track **who** does each, we can correlate:

```
Parent (Crossplane XR)
  status updaters:  [user-hash-A]       <-- who calls /status

Child (ManagedResource)
  spec updaters:    [user-hash-A, user-hash-B]  <-- who modifies spec
```

**Intersection** of parent status updaters and child spec updaters = the controller.

Works for **any** controller. No special integration needed.

---

## The Detection Model

```
Request arrives: user U wants to mutate child C

1. Find parent P via controller ownerReference

2. Is U the controller?
   - Single child updater? That's the controller.
   - Multiple? Intersect child updaters with parent status updaters.
   - Can't tell? Be lenient.

3. If U is not the controller:
   --> new causal origin (different actor, different intent)

4. If U is the controller:
   - P.generation != P.observedGeneration --> reconciling, expected
   - P.generation == P.observedGeneration --> DRIFT
```

---

## Challenge 2: What Is "Stable"?

The detection hinges on: **is the parent stable?**

`generation == observedGeneration` should tell us. But:

- Some resources **don't have** `status.observedGeneration`
  (e.g., CAPI Clusters, older CRDs)
- Crossplane stores `observedGeneration` inside conditions, not at the top level
- Some controllers never set it at all

We need a **fallback**.

---

## Synthetic observedGeneration

**Key insight**: when a controller updates status, it has necessarily **observed** the current generation.

If we intercept status updates, we can record:

```yaml
kausality.io/observedGeneration: "5"   # set on each status update
```

Precedence for determining stability:

```
1. status.observedGeneration              (native)
2. Condition-level observedGeneration     (Crossplane-style)
3. kausality.io/observedGeneration        (synthetic fallback)
```

Works with **any** controller. No modifications needed.

---

## Challenge 3: Lifecycle Phases

Not every phase of a resource's life should trigger drift detection:

| Phase | What's happening | Detection? |
|-------|-----------------|------------|
| **Initializing** | Children being created for the first time | No -- everything is new |
| **Initialized** | System is stable, steady state | **Yes** |
| **Deleting** | Finalizers cleaning up | No -- teardown is expected |

Initialization detected via:
- `Ready=True` or `Initialized=True` conditions
- `observedGeneration == generation` (controller caught up)
- Persisted `kausality.io/phase: initialized` annotation (survives flapping)

---

## Causal Traces

Once we can detect drift, we can do more: **trace causality** through the hierarchy.

Every mutation gets a `kausality.io/trace` -- the causal chain:

```json
[
  {
    "kind": "Deployment", "name": "prod", "generation": 5,
    "user": "hans@example.com",
    "labels": {"ticket": "JIRA-123"}
  },
  {
    "kind": "ReplicaSet", "name": "prod-abc",
    "user": "system:serviceaccount:kube-system:deployment-controller"
  }
]
```

Every child change links back to the **human intent** that started it.

---

## Origin vs Hop

Two things can happen when a mutation arrives:

**New origin** (start a fresh trace):
- No controller ownerReference
- Actor is not the controller
- Parent is stable (`gen == obsGen`) -- drift case

**Hop** (extend the parent's trace):
- Has controller ownerReference
- Actor is the controller
- Parent is reconciling (`gen != obsGen`)

GitOps tools, HPA, humans --> always **origins** (new intent).
Kubernetes controllers --> **hops** (propagating existing intent).

---

## The Full Picture

```
SRE edits Crossplane XR spec
  |
  |  kubectl apply (origin: sre@company.com, JIRA-456)
  v
XR EKSCluster (gen: 6, obsGen: 5)  -- reconciling
  |
  |  crossplane-composition (hop: extends trace)
  v
ManagedResource NodeGroup (trace: XR -> NodeGroup)
  |
  |  provider-aws (hop: extends trace)
  v
AWS API call (trace: XR -> NodeGroup -> AWS)
```

The AWS change knows **who** edited the XR and **why**.
Same pattern for ACK, OpenTofu, native controllers -- any hierarchy.

---

## What Drift Looks Like

```
Nothing changes in the OpenTofu Workspace spec.

But the controller runs a periodic re-sync.

  opentofu-controller runs tofu plan (gen == obsGen!)
  |
  v
Workspace status UPDATE  <-- plan is non-empty
  |
  |  "Controller wants to apply, but nobody asked it to."
  |  "Parent is stable. This change has no cause."
```

Same thing with ACK finding AWS console changes, Crossplane re-rendering compositions, or a Deployment controller reacting to a ConfigMap change.

---

## The Boundary: KRM vs External World

Kausality operates on **controllers doing KRM mutations downward** -- parent CRD to child CRD. Admission sees every such mutation. This is where the model works out of the box.

But many hierarchies end with a **leaf controller** that talks to the outside world:

```
    KRM world (admission sees everything)       External world
 ──────────────────────────────────────────── ┊ ──────────────────
 Crossplane XR -> Composition -> ManagedRes  ┊-> AWS API -> S3
 OpenTofu Module -> Workspace                ┊-> tofu apply -> VPC
 ACK DBInstance                              ┊-> AWS API -> RDS
```

The leaf controller is the **gateway**. Once it calls an external API, Kausality can't see what happens.

**Integration needed**: the leaf controller must understand the drift signal. If kausality says "this reconciliation is drift", the controller should hold back the external API call -- not just the KRM mutation.

---

## Putting It All Together

The model gives us a **formal structure** over controller hierarchies:

1. **Actor identification** via user hash correlation
2. **Stability detection** via generation/observedGeneration (with synthetic fallback)
3. **Change classification**: expected reconciliation, drift, or new origin
4. **Causal tracing**: every mutation linked to its root cause
5. **Lifecycle awareness**: initialization and deletion are not drift

All of this runs in **admission** -- no controller changes, works with any existing hierarchy.

---

## What Can We Do With This?

Once you can **detect and classify** every change:

- **Log** drift to understand your landscape
- **Enforce** approval for unexpected changes
- **Trace** incidents back to root cause across the full hierarchy
- **Audit** every decision independently (survives object deletion)
- **Approve** known-safe drift patterns
- **Freeze** resources during incidents

The detection model is the foundation. Policy and enforcement are layers on top.

---

## Key Takeaways

1. **Declarative != safe** -- convergence can destroy intended state. Crossplane, ACK, OpenTofu, native controllers -- all the same.

2. **Three types of changes**: reconciliation, drift, new origin -- you need to distinguish them

3. **Causality is the missing concept** -- Kubernetes tracks *what*, not *why*

4. **KRM detection works today** -- admission sees every mutation inside the API server

5. **External systems need integration** -- controllers are the gateway to cloud APIs; they must understand the drift signal

---

<!-- _class: lead -->
<!-- _paginate: false -->

# Thank You

**github.com/kausality-io/kausality**

*"Every mutation needs a cause."*

Questions?
