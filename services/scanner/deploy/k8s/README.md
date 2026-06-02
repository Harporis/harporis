# Scanner — Kubernetes manifests

Static manifests for a `replicas: 4` Deployment. Apply with:

    kubectl apply -f deployment.yaml -f service.yaml

If you run kube-prometheus / Prometheus Operator, also apply:

    kubectl apply -f servicemonitor.yaml

## Scaling

Edit `spec.replicas` in `deployment.yaml` and re-apply. Or:

    kubectl scale deploy/harporis-scanner --replicas=8

NATS handles the round-robin: all replicas share the `scanner-pool`
durable consumer on `HARPORIS_CHUNKS`.

## HPA (optional, not applied by default)

Sample HorizontalPodAutoscaler targeting CPU 70%:

    kubectl autoscale deploy/harporis-scanner --cpu-percent=70 --min=2 --max=20

For throughput-based autoscaling, use a custom metric (`scanner_chunks_consumed_total`
rate) via Prometheus Adapter.
