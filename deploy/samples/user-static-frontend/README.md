# Sample user app (static frontend)

This is a **minimal example** of what users deploy through `POST /deploy-app`: any repository with a **Dockerfile** in the root can run in `user-apps` and get a public URL (HTTPRoute/Ingress).

## Try it

1. Push this directory to a **public** Git repository (or use your private repo with credentials you already use for Kaniko).
2. Call the orchestrator with `githubRepoEndpoint` set to `https://github.com/you/your-repo` (and optional `gitRef`).
3. Set orchestrator env **`APP_CONTAINER_PORT=80`** and **`K8S_SERVICE_PORT=80`** for this image (nginx listens on 80).

## SPA / React / Vue

Use a multi-stage Dockerfile: build with Node, then copy `dist/` into `nginx:alpine` (or `serve` on `APP_CONTAINER_PORT`). The platform does not special-case “frontend” — the container must listen on the configured port.
