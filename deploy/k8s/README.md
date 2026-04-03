# Kubernetes manifests (reference)

Apply in order:

```bash
kubectl apply -f namespace.yaml
kubectl apply -f rbac.yaml
```

- **Namespace** `user-apps` must exist before the orchestrator runs (`K8S_NAMESPACE=user-apps`).
- **RBAC** binds **ServiceAccount** `go-vercel-orchestrator` in **user-apps** to a **Role** in `user-apps` (Jobs, Deployments, Services, Ingresses, **HTTPRoutes**).
- **Harbor credentials** for Kaniko are created at runtime as Secret `harbor-regcred` in `user-apps` from `HARBOR_*` env vars (no manifest needed for that secret).

## Ingress / Gateway

Install an ingress controller **or** use Istio Gateway API.

- `INGRESS_BASE_DOMAIN` (e.g. `launchpad.neev.work`)
- `INGRESS_CLASS_NAME` when using classic Ingress
- `GATEWAY_NAME` / `GATEWAY_NAMESPACE` / `GATEWAY_SECTION_NAME` when using **HTTPRoute** (see `deploy/vars.env.example`)

DNS: `*.{INGRESS_BASE_DOMAIN}` must resolve to your edge (nginx, load balancer, or NodePort).

## Run the orchestrator in the cluster

1. Build and push the image (from **repository root**):

   ```bash
   docker build -f deploy/Dockerfile -t YOUR_REGISTRY/hackthon/go-vercel-orchestrator:latest .
   docker push YOUR_REGISTRY/hackthon/go-vercel-orchestrator:latest
   ```

2. Edit `orchestrator-deployment.yaml` if your image name differs.

3. Create secrets (do not commit real files):

   ```bash
   cp orchestrator-secrets.example.yaml orchestrator-secrets.yaml
   # edit orchestrator-secrets.yaml, then:
   kubectl apply -f orchestrator-secrets.yaml
   ```

4. Apply ConfigMap and Deployment:

   ```bash
   kubectl apply -f orchestrator-configmap.yaml
   kubectl apply -f orchestrator-deployment.yaml
   ```

The Service DNS name inside the cluster is:

`http://go-vercel-orchestrator.user-apps.svc.cluster.local:8081`

User applications (any Dockerfile: APIs, SPAs, static sites) are deployed via **`POST /deploy-app`** on the orchestrator (see `deploy/scripts/test-deploy-app.sh` and `deploy/samples/user-static-frontend/`).

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
