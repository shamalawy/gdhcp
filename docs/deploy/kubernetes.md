# Kubernetes Deployment

`deploy/kubernetes/` is a Kustomize base + overlays: a `gdhcp` DaemonSet
(API, UI, and DHCP engine), a `gdhcp-postgres` StatefulSet (optional — swap
for a managed database), and the ConfigMap/Secret/Service wiring between
them.

```
deploy/kubernetes/
├── kustomization.yaml        # thin pointer at base/, for quick local applies
├── base/
│   ├── namespace.yaml
│   ├── postgres/              # configmap (init SQL), secret, statefulset, service
│   └── gdhcp/                 # configmap (config.yaml), secret (jwt), daemonset, service
└── overlays/
    ├── production/            # real secrets, bigger resource limits, storage class
    └── staging/                # real secrets, base's resource limits otherwise
```

## Deploy

```bash
# quick local smoke test against base's placeholder secrets (do NOT use for
# anything real — deploy/kubernetes/base/*/secret.yaml ship `changeme`):
kubectl apply -k deploy/kubernetes/base

# real environments: create the per-overlay secret env files first (both
# gitignored, never commit them)
cat > deploy/kubernetes/overlays/production/.env.secret <<EOF
username=gdhcp
password=$(openssl rand -base64 24)
database=gdhcp
dsn=postgres://gdhcp:<same password>@gdhcp-postgres:5432/gdhcp?sslmode=disable
EOF
echo "jwt_secret=$(openssl rand -base64 32)" > deploy/kubernetes/overlays/production/.jwt.secret

kubectl label node <node> gdhcp.io/dhcp-node=true
kubectl apply -k deploy/kubernetes/overlays/production

kubectl get pods -n gdhcp -o wide
kubectl logs -n gdhcp -l app.kubernetes.io/component=server -f
kubectl port-forward -n gdhcp svc/gdhcp-api 8080:8080   # to reach the UI/API without a node IP
```

Repeat the same for `overlays/staging`, ideally against a separate cluster
or at minimum a disjoint `gdhcp.io/dhcp-node` node pool — see the comment in
that overlay's `kustomization.yaml` for why running staging and production
DHCP on the same broadcast segment is a bad idea regardless of namespacing.

## Why a DaemonSet, not a Deployment

DHCP needs `hostNetwork: true` to see broadcast traffic on the node's real
interfaces (no CNI forwards L2 broadcast into a pod network namespace).
Once a pod must be pinned to a specific node's network stack, "replica
count" stops being a scaling knob and becomes "which nodes should answer
DHCP" — which is exactly what a DaemonSet expresses natively via
`nodeSelector: {gdhcp.io/dhcp-node: "true"}`. A Deployment would need the
same node affinity bolted on manually with none of the "add a node, get a
pod automatically" behavior DaemonSet gives for free.

## Caveats specific to Kubernetes

- **`hostNetwork: true` is required, not a hardening trade-off you can opt
  out of.** The pod shares the node's network namespace entirely — it can
  reach every interface and service the node can. Acceptable for this
  trusted infrastructure workload; do not reuse this pattern for
  general-purpose pods.
- **Node selection matters.** Only label nodes with `gdhcp.io/dhcp-node=true`
  if they have an actual L2 link to the network DHCP clients live on —
  labeling a node behind a router or in the wrong VLAN gets you a pod that
  never receives client broadcasts.
- **VIP failover does not work natively in Kubernetes.** `ha.vip_address`
  assumes gratuitous ARP on a shared L2 segment, which is a bare-metal/VM
  networking primitive Kubernetes' Service abstraction does not provide.
  Options, in order of how much extra infrastructure they need:
  1. Run `keepalived` per DHCP-serving node (outside or alongside the
     container) to manage the VRRP VIP the same way bare metal does —
     `gdhcp`'s own election/heartbeat logic is unaffected either way.
  2. Point clients at a cloud load balancer with health checks instead of a
     VIP (works for the HTTP API; still does not solve DHCP broadcast).
  3. Keep DHCP itself on bare metal (see [bare-metal.md](bare-metal.md)) and
     use Kubernetes only for the API/UI tier, pointed at the same database.
- **`gdhcp-postgres` is a single-replica StatefulSet with no automatic
  failover.** Fine for evaluation; for production either point
  `gdhcp-db-credentials`'s `dsn` at a managed Postgres instance and drop
  `base/postgres/*` from the overlay's `resources`, or run your own HA
  Postgres setup (Patroni, etc.) in front of the same Secret contract.
- **Health probes** hit `GET /api/v1/health` on port 8080 — same endpoint
  Prometheus and any external load balancer should use.

## Updating

```bash
kustomize edit set image gdhcp=my-registry.example.com/gdhcp:v1.1.0   # inside overlays/production/
kubectl apply -k deploy/kubernetes/overlays/production
kubectl rollout status daemonset/gdhcp -n gdhcp
```

DaemonSet rollout uses `maxUnavailable: 1` — one node's DHCP service is
briefly interrupted per step, not the whole fleet at once.
