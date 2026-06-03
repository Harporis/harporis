# Writer — Kubernetes manifests

Apply with kubectl or kustomize:

```bash
kubectl apply -f services/writer/deploy/k8s/
```

Inside one namespace (e.g. `harporis`).

## What ships

- `pvc.yaml` — `PersistentVolumeClaim` for NDJSON findings storage. Update
  `storageClassName` to match your cluster. With the default
  `ReadWriteOnce` access mode, keep `replicas: 1` in `deployment.yaml`.
- `deployment.yaml` — single-replica Deployment with the findings PVC
  mounted at `/var/lib/harporis/findings`. Pod-level security context
  drops all caps + runs non-root + uses the runtime-default seccomp.
- `service.yaml` — headless Service (clusterIP: None) on port 9102
  for Prometheus scraping. No `LoadBalancer` / `NodePort` is created;
  writer findings are read from the PVC, not the network.
- `servicemonitor.yaml` — Prometheus Operator `ServiceMonitor` scraping
  every 15s. Apply only if the Prometheus Operator CRD is installed.
- `networkpolicy.yaml` — egress allowlist: NATS:4222 and cluster DNS.
  Ingress: only the monitoring namespace can scrape /metrics.

## Scaling notes

The default PVC access mode is `ReadWriteOnce`, which means at most ONE
pod can mount it at a time — so `replicas: 1`. If you need N replicas:

- Switch the PVC to a `ReadWriteMany` storage class (CephFS, NFS, EFS,
  Longhorn RWX, ...).
- Bump `replicas` in `deployment.yaml`.
- Findings stay correctly serialized: the writer uses one O_APPEND file
  per scan_id with per-file mutex, and the kernel linearizes write(2)
  calls up to PIPE_BUF (typically 4096B) on the same fd across processes.
  Each writer replica opens its own fd, so torn lines remain extremely
  rare even without explicit cross-replica coordination.
- The `WriterPoolQueueGroup` + `WriterDurableConsumer` give exactly-once
  delivery PER MESSAGE across replicas: scaling out increases throughput
  but does not duplicate findings.

## Reading findings

```bash
# From the cluster (preferred):
kubectl exec -n harporis deploy/harporis-writer -- cat /var/lib/harporis/findings/<scan_id>.ndjson

# From the host via the CLI:
harporis findings show <scan_id>   # uses docker compose exec in dev;
                                   # kubectl exec under k8s (planned)
```

## Verify

```bash
kubectl -n harporis get pods -l app=harporis-writer
kubectl -n harporis exec deploy/harporis-writer -- wget -qO- http://localhost:9102/metrics | head -20
```

`writer_findings_consumed_total`, `writer_sink_writes_total{sink,severity}`,
and `writer_build_info` should be present.
