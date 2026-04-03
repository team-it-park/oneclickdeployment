# Minimal user-facing static site (HTML/CSS/JS). Serves on port 80.
# Point APP_CONTAINER_PORT=80 and K8S_SERVICE_PORT=80 in the orchestrator when deploying this image.
FROM nginx:1.25-alpine
COPY index.html /usr/share/nginx/html/index.html
EXPOSE 80
