# Go-Vercel-App

A small **Vercel-style** deployment demo in Go: users sign in, submit a **public Git repository URL**, and the platform **builds a container image in Kubernetes (Kaniko)**, **pushes to Harbor**, **runs the app in the cluster**, and exposes a **public URL via Ingress**.

## Architecture

1. **Vercel** ([`vercel/`](vercel/)) — Echo web UI, sessions, GitHub OAuth and email auth. On deploy, the browser opens a WebSocket; the server calls the orchestrator over **HTTP** (no RabbitMQ).
2. **Orchestrator** ([`deploy/`](deploy/)) — Echo HTTP API **`POST /deploy-app`** (alias `POST /build-deploy`). Uses **client-go** to create a **Kaniko Job**, wait for success, then apply **Deployment**, **Service**, and **Ingress** or **HTTPRoute**. User repos must include a **Dockerfile** (SPA, API, or static site). Harbor credentials are stored as a **dockerconfigjson** Secret in the workload namespace.
3. **Traffic** — End users hit `{projectID}.{INGRESS_BASE_DOMAIN}` through your ingress controller, not a separate static-file service.

```mermaid
flowchart LR
  User[User] --> Vercel[Vercel web]
  Vercel -->|HTTP NDJSON| Orch[Orchestrator]
  Orch --> K8s[Kubernetes]
  Orch --> Harbor[Harbor]
  K8s --> Ingress[Ingress]
  User --> Ingress
```

## Repository contract (v1)

The Git repo must include a **Dockerfile** that runs an HTTP server on **`APP_CONTAINER_PORT`** (default **8080**). That can be a **frontend** (static files + nginx, or Node `serve`), an API, or anything containerized. See [`deploy/k8s/Dockerfile.sample`](deploy/k8s/Dockerfile.sample) and [`deploy/samples/user-static-frontend/`](deploy/samples/user-static-frontend/).

## Environment variables

Templates you can copy: [`deploy/vars.env.example`](deploy/vars.env.example) and [`vercel/vars.env.example`](vercel/vars.env.example) → `vars.env` in each directory (real `vars.env` is gitignored).

### Vercel (`vercel/vars.env`)

| Variable | Purpose |
|----------|---------|
| `ADDR` | Listen address (e.g. `:8080`) |
| `SECRET` | Session encryption key |
| `DBADDRESS` | PostgreSQL DSN for GORM |
| `CLIENT_ID` / `CLIENT_SECRET` / `GITHUB_OAUTH_CALLBACK_PATH` | GitHub OAuth |
| `APP_EMAIL` / `APP_PASSWORD` / `SMTP_HOST` / `SMTP_PORT` | Email verification |
| **`ORCHESTRATOR_ADDR`** | Base URL of orchestrator (e.g. `http://localhost:8081`) |
| **`ORCHESTRATOR_DEPLOY_PATH`** | Optional path (default **`/deploy-app`**; legacy **`/build-deploy`**) |
| **`ORCHESTRATOR_SHARED_SECRET`** | Optional; must match orchestrator if set |
| **`ORCHESTRATOR_DEFAULT_GIT_REF`** | Optional git ref (default on orchestrator side: `refs/heads/main`) |
| **`ORCHESTRATOR_HTTP_TIMEOUT_MINUTES`** | Optional HTTP client timeout (default 45) |

### Orchestrator (`deploy/vars.env`)

| Variable | Purpose |
|----------|---------|
| `ADDR` | Listen address (e.g. `:8081`) — serves **`POST /deploy-app`** (and **`POST /build-deploy`**) |
| **`K8S_NAMESPACE`** | Namespace for Jobs and workloads (must exist; apply [`deploy/k8s/namespace.yaml`](deploy/k8s/namespace.yaml)) |
| **`HARBOR_REGISTRY`** | Registry host (no scheme), e.g. `harbor.example.com` |
| **`HARBOR_PROJECT`** | Harbor project name (default `go-vercel-apps`) |
| **`HARBOR_USERNAME`** / **`HARBOR_PASSWORD`** | Push/pull registry auth |
| **`INGRESS_BASE_DOMAIN`** | e.g. `apps.example.com` → host `{projectID}.apps.example.com` |
| `INGRESS_CLASS_NAME` | Ingress class (e.g. `nginx`) |
| `INGRESS_TLS_SECRET_NAME` | Optional TLS secret in the same namespace as Ingress |
| `KUBECONFIG` | Optional; if unset, uses in-cluster config then `~/.kube/config` |
| `ORCHESTRATOR_SHARED_SECRET` | Optional shared header `X-Orchestrator-Secret` |
| `KANIKO_IMAGE` | Executor image (default `gcr.io/kaniko-project/executor:v1.23.2`) |
| `APP_CONTAINER_PORT` | Container port (default `8080`) |
| `BUILD_JOB_TIMEOUT_SEC` | Kaniko job wait timeout (default `1800`) |
| `ORCHESTRATOR_SKIP_HARBOR_TLS_VERIFY` | Set `true` for self-signed Harbor (Kaniko `--skip-tls-verify`) |
| `DEFAULT_GIT_REF` | Git ref for Kaniko context (default `refs/heads/main`) |

## Kubernetes

See [`deploy/k8s/README.md`](deploy/k8s/README.md) for namespace, RBAC, and a manual Kaniko example. End-to-end checks: [`deploy/k8s/VERIFY.md`](deploy/k8s/VERIFY.md).

## Legacy services (not used by the new flow)

The **[`upload/`](upload/)** (MinIO + RabbitMQ) and **[`request-handler/`](request-handler/)** (static files from MinIO) trees are **legacy** from the previous architecture. The active path is **Vercel → orchestrator → K8s/Harbor**.

## Tools

- Go, PostgreSQL, Kubernetes, Harbor, Ingress controller, Kaniko (pulled as Job image)
