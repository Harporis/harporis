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
