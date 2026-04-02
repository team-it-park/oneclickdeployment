# End-to-end verification (kind / minikube)

Use this checklist to confirm the full pipeline after configuring Harbor, DNS, and ingress.

## Prerequisites

- Cluster with **nginx-ingress** (or similar), **wildcard DNS** for `*.apps.example.com` (replace with your `INGRESS_BASE_DOMAIN`).
- **Harbor** project (e.g. `go-vercel-apps`) and a user or robot account with push/pull rights.
- `kubectl` and `KUBECONFIG` pointing at the cluster (or run orchestrator inside the cluster with the RBAC from `rbac.yaml`).

## 1. Cluster baseline

```bash
kubectl apply -f namespace.yaml
kubectl apply -f rbac.yaml
kubectl get ns user-apps
```

## 2. Orchestrator

Build and run the orchestrator with `deploy/vars.env` filled (see root README). From repo root:

```bash
cd deploy && make build && ./bin/deploy
```

Or run the binary where it can reach the API server (`KUBECONFIG` or in-cluster SA).

## 3. Vercel

```bash
cd vercel && make build && ./bin/vercel
```

Set `ORCHESTRATOR_ADDR` to the orchestrator’s reachable URL (e.g. `http://127.0.0.1:8081` if both run locally).

## 4. API smoke test

```bash
curl -sS -X POST "http://127.0.0.1:8081/build-deploy" \
  -H "Content-Type: application/json" \
  -H "X-Orchestrator-Secret: YOUR_SECRET_IF_SET" \
  -d '{"githubRepoEndpoint":"https://github.com/OWNER/REPO","projectID":"test1","gitRef":"refs/heads/main"}'
```

Expect NDJSON lines ending with `{"success":true,"publicURL":"..."}`. The repo must contain a valid Dockerfile listening on your `APP_CONTAINER_PORT`.

## 5. UI flow

1. Sign in at the Vercel app.
2. Submit a public repo URL on the home form.
3. Watch the processing page WebSocket for `building`, `deploying`, `deployed`, then the printed URL.

## 6. Confirm routing

```bash
curl -sS -o /dev/null -w "%{http_code}" --resolve "test1.apps.example.com:443:INGRESS_IP" \
  https://test1.apps.example.com/
```

Adjust host, scheme, and SNI to match your ingress and TLS setup.
