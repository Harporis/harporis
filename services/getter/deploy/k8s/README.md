# Getter — Kubernetes manifests

Static manifests for a `replicas: 2` Deployment. Apply with:

    kubectl apply -f deployment.yaml -f service.yaml

The work directory uses `emptyDir` (per-pod scratch) rather than a shared
PersistentVolume — N replicas writing to the same path would race.
emptyDir is ephemeral, which matches the getter's lifecycle (workspaces
are cleaned up after each scan).

Scaling: `kubectl scale deploy/harporis-getter --replicas=N`. The shared
durable consumer (`getter-pool`) on the `HARPORIS_REQUESTS` work-queue
stream distributes scan requests round-robin.

## NetworkPolicy

`networkpolicy.yaml` restricts pod egress to NATS, cluster DNS, and
outbound HTTPS/SSH (for `git clone`). Ingress is restricted to Prometheus
scrape + the internal gRPC port. Apply only if your cluster has a
NetworkPolicy controller (Calico, Cilium, etc.):

    kubectl apply -f networkpolicy.yaml

On clusters without one, the YAML is a no-op — defense in depth, not a
hard guarantee. Production deployments should narrow the HTTPS/22 egress
rules to an allowlist of git mirrors.

## Pod Security Standards

The Deployment ships with a restricted profile:

  - `runAsNonRoot: true` + `seccompProfile: RuntimeDefault`
  - `allowPrivilegeEscalation: false`
  - `capabilities: { drop: [ALL] }`

`readOnlyRootFilesystem` is intentionally **not** set — `git clone`
writes pack indexes, FETCH_HEAD, and ref files to the working tree and
`$TMPDIR`, and locking down the rootfs breaks clones. The scratch
`emptyDir` mount already isolates the on-disk workspace per pod.
