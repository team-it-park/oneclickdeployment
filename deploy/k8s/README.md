# Kubernetes manifests (reference)

Apply in order:

```bash
kubectl apply -f namespace.yaml
kubectl apply -f rbac.yaml
```

- **Namespace** `user-apps` must exist before the orchestrator runs (`K8S_NAMESPACE=user-apps`).
- **RBAC** binds `ServiceAccount` `go-vercel-orchestrator` in `default` to a **Role** in `user-apps`. Adjust namespaces if you run the orchestrator elsewhere.
- **Harbor credentials** are created at runtime as Secret `harbor-regcred` in `user-apps` from `HARBOR_*` env vars (no manifest needed for that secret).

## Ingress

Install an ingress controller (e.g. nginx-ingress) and set:

- `INGRESS_BASE_DOMAIN` (e.g. `apps.example.com`)
- `INGRESS_CLASS_NAME` to match your controller
- Optional `INGRESS_TLS_SECRET_NAME` for HTTPS (wildcard cert in the same namespace as Ingress)

DNS: `*.{INGRESS_BASE_DOMAIN}` must resolve to the ingress load balancer.

## Manual Kaniko test

Edit placeholders in `example-kaniko-job.yaml`, create `harbor-regcred` in `user-apps`, then:

```bash
kubectl apply -f example-kaniko-job.yaml
kubectl logs -n user-apps -f job/kaniko-manual-test
```

## Sample Dockerfile

See `Dockerfile.sample` for a minimal image that serves on port 8080. User repositories must ship a **Dockerfile** that exposes the app on the port configured by `APP_CONTAINER_PORT` (default **8080**).

## End-to-end checklist

See [VERIFY.md](VERIFY.md) for kind/minikube-style verification steps.
