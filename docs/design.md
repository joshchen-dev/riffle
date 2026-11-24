# Riffle design

Log rules as Kubernetes resources. Teams declare `LogAlert`s. A node agent matches them against pod logs, and the operator turns matches into metrics, Events and status. The `riffle` CLI tails the same logs ad hoc and reuses the same rules.

## Architecture

    ┌──────────── control plane ────────────┐
    │ riffle-operator (Deployment, 1 rep)   │
    │  - validates LogAlerts → .status      │
    │  - aggregates counts → Firing, Events │
    └───────▲───────────────────────────────┘
            │ POST /v1/counts (10s buckets)
    ┌───────┴──── each node ────────────────┐
    │ riffle-agent (DaemonSet)              │
    │  - watches LogAlerts + pods on node   │
    │  - tails /var/log/pods (read-only)    │
    │  - match → redact → count, /metrics   │
    └───────────────────────────────────────┘
    riffle CLI ── pods/log API ── any cluster (operator optional)

- `pkg/filter`: shared by agent and CLI. Regex (RE2) + `where` expressions + redaction. That makes CLI filters and LogAlerts mean the same thing.
- Agents read LogAlerts and their own node's pods from an informer cache and reconcile file followers every 2s. No compiled ConfigMap in between.
- One image holds both binaries: `/manager` and `/agent`.
- Thresholds are cluster-wide, so per-node counting can't decide them. Agents push per-rule bucket counts, and the operator sums them over the window.

## CRD: `LogAlert` (`riffle.jhchen.dev/v1alpha1`, namespaced)

Namespaced: a rule only selects pods in its own namespace. Team RBAC then maps onto rules directly.

    apiVersion: riffle.jhchen.dev/v1alpha1
    kind: LogAlert
    metadata: {name: api-timeouts, namespace: payments}
    spec:
      selector:                    # metav1.LabelSelector, required
        matchLabels: {app: api}
      container: api               # optional; default all
      match: 'level=error .* timeout'          # RE2, optional
      where: 'level=error AND latency_ms>500'  # JSON field expr, optional
      threshold: {count: 20, window: 5m}       # count ≥1, window 1m–1h
      redact: ['card=\d+']         # RE2, replaced with [REDACTED]
      suspend: false
    status:
      observedGeneration: 3
      matchedPods: 4
      lastMatchTime: ...
      lastFiredTime: ...
      conditions:
        - type: Ready     # spec compiles and agents run it
          reasons: Valid | InvalidSpec | Suspended
        - type: Firing    # window count ≥ threshold
          reasons: ThresholdExceeded | BelowThreshold

- At least one of `match`/`where` is required (CEL rule on the CRD).
- Regex validity is checked by the reconciler (CEL can't compile RE2). No webhook in v1alpha1.
- `where` grammar: `field op value` joined with `AND`/`OR`/`NOT` and parentheses. Ops: `= != > >= < <= ~`. Fields are dotted JSON paths. Non-JSON lines never match a `where`.

## Reconciliation

LogAlert controller (watches LogAlert, and Pods mapped to the LogAlerts that select them):
1. Get the LogAlert. If it's gone, stop. No finalizer is needed because nothing external is owned.
2. Compile `match`, `where`, `redact`. On error: Ready=False/InvalidSpec with the message, then stop.
3. `suspend` → Ready=False/Suspended.
4. Count the selected Running pods → `matchedPods`.
5. Ready=True/Valid. Set `observedGeneration`.
6. Status patch only. Requeue: none. Events and the Pod watch drive it.

Agent deployment: static manifests in `config/agent` (namespace with privileged Pod Security, ServiceAccount, read-only ClusterRole, DaemonSet), installed by `make deploy` / Argo CD. The operator doesn't create the DaemonSet or grant RBAC, so it never needs `escalate`/`bind`, and everything stays in Git. Upgrade path if scale-to-zero matters: an operator-managed DaemonSet.

Aggregator (runs in the operator):
- Each agent counts matches per rule in 10s buckets and POSTs them to `riffle-counts:8082/v1/counts` within 1s of a new match. Nothing is sent while nothing matches.
- The current bucket is resent until it closes; a resent bucket replaces the old value, so retries don't double count. Unsent buckets are kept for 1h.
- Auth: a projected ServiceAccount token with audience `riffle-operator`, checked with a TokenReview. Only the agent's ServiceAccount is accepted. The token can't be used against the API server.
- New counts trigger a reconcile, which sums the rule's buckets over its window. While buckets exist it re-evaluates every 10s, so alerts resolve as the window slides.
- Reaching the threshold sets Firing=True and `lastFiredTime`, and records one Warning Event `ThresholdExceeded` on the LogAlert with the redacted sample (max 256 bytes).
- Dropping below sets Firing=False and records a Normal Event `Resolved`. A rule that isn't Ready is `NotEvaluated`.
- Webhook: on firing and resolved, the operator POSTs JSON to the `url` key of Secret `riffle-webhook` in its namespace. The body has a Slack-compatible `text`, plus `status`, `namespace`, `rule`, `count`, `threshold`, `window`, `sample`. 3 attempts, then the alert is dropped. The operator can read only that one Secret.
- Latency, log line to webhook (`hack/bench-latency.sh`, k3d, 20 trials): p50 502 ms, max 524 ms. Most of it is the agent's 250ms file poll plus the 1s push tick.
- Ceiling: the window lives in memory, so an operator restart resets it. That's acceptable for windows of 1h or less.

## Reading logs: `/var/log/pods` vs kubelet API

**Recommendation: `/var/log/pods` via read-only hostPath.**

| | `/var/log/pods` | kubelet `/containerLogs` |
|---|---|---|
| RBAC | pods list/watch (own node) | `nodes/proxy`, which is effectively cluster-admin on the node |
| Cost | file reads | one HTTP stream per container through kubelet |
| Restart recovery | resume from saved offsets | re-stream with `sinceTime`, gaps or dupes |
| Precedent | Fluent Bit, Vector, Promtail | rare for agents |
| Downside | parse CRI format, handle rotation, privileged PSA namespace | none of that |

- Path: `/var/log/pods/<ns>_<pod>_<uid>/<container>/<n>.log`. The agent maps uid → pod labels using a pod informer filtered by `spec.nodeName`.
- CRI line format: `<ts> <stream> <P|F> <msg>`. Join `P` partial lines until `F`.
- Rotation: detect it by inode change or truncation, then finish the old file before switching. Offsets are kept in memory in v1. A restart resumes from EOF and may skip lines.
- Only containerd/CRI-O are supported. dockershim is gone.

## CLI

Standalone, it uses kubeconfig and the `pods/log` API. If it finds the `riffle.jhchen.dev` CRD, it enables the operator-backed features.

    riffle tail [-n ns] [-l sel] [-c container] [--match re] [--where expr]
                [--since 1m] [-i]
    riffle tail --rule <name>          # selector/match/where/redact from a LogAlert
    riffle alert new <name> -l sel --match .. --where .. --count 20 --window 5m [--create]
                                       # prints YAML (for Git) unless --create
    riffle alerts [-n ns | -A]         # Ready, Firing, pods, threshold, last match
    riffle version

- Timeline: streams use `timestamps=true` and go through a 250ms reorder buffer, then the CLI prints one merged, ordered output with a pod/container prefix.
- Pods are watched, so pods created after the start (rollouts, restarts) are picked up.
- Redaction: when the operator is installed, the CLI applies the `redact` patterns of every LogAlert that selects the pod. LogAlerts are read once at start. If they can't be read, `tail` warns and runs without them; `--rule` fails.
- `-i`: a TUI (bubbletea) with an editable filter line. The `a` key exports the current filter as LogAlert YAML.

## Metrics

- Agent:
  - `riffle_matches_total{namespace,rule,container}`
  - `riffle_lines_total`, `riffle_bytes_total`
  - Invalid rules are skipped; the operator reports them as `InvalidSpec`.
- Operator:
  - `riffle_alert_firing{namespace,rule}` (gauge)
  - `riffle_alert_window_count{namespace,rule}`
  - controller-runtime defaults
- No pod label, to cap cardinality.

## Non-goals

- Log storage, search and retention. Use Loki or ES.
- Shipping all logs. Only matches leave the node, and only to S3 from M7.
- Sources outside pods (journald, files on the host).
- Multi-cluster, web UI.
- Redaction as a security boundary. Anyone with `pods/log` still sees raw lines.
- Docker runtime.

## Milestones

1. **M0** scaffold: kubebuilder layout, CLI skeleton, Makefile, CI (lint, unit, envtest, image), README, VERSION check.
2. **M1** CRD + LogAlert controller (validation, status), envtest coverage.
3. **M2** `riffle tail` standalone: multi-pod merge, `--match`, `--where`, `pkg/filter`.
4. **M3** agent: CRI tailing, rotation, metrics. DaemonSet from static manifests. Runs on k3d.
5. **M4** aggregation: Firing condition, Events.
6. **M5** CLI ↔ operator: `--rule`, redaction, `alert new`, `alerts`.
7. **M6** `tail -i` TUI.
8. **M7** S3 archive of redacted matches. Bucket, IAM and lifecycle in Terraform.
9. **M8** homelab: RHEL/k3s, Argo CD app-of-apps, Riffle watching k3s/Argo CD/Traefik pods.

## Risks

- **Agent CPU at high volume.** Every line is checked against every rule that selects the pod. Mitigations: index rules per pod, use literal prefilters, count dropped lines.
- **Rotation/partial-line bugs.** Covered by a fixture-based test suite for CRI files.
- **Operator as single aggregator.** Alerts are late while it is down (agents hold counts for 1h), and the window resets on restart. Upgrade path: leader-elected HA with agents pushing to the leader Service.
- **Plain HTTP counts endpoint.** A sniffed token lets someone push fake counts until it expires (10 min). Upgrade path: TLS on the endpoint.
- **hostPath + privileged PSA** on the agent namespace. Mitigations: mount read-only, read-only root filesystem, drop all capabilities.
- **Cardinality.** User-controlled rule names in labels, bounded by the number of LogAlerts.
- **S3 creds on k3s.** No IRSA there. Use IAM Roles Anywhere or a scoped IAM user. Decide at M7.
