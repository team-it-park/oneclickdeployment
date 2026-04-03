## Orchestrator API — builds user apps via Kubernetes + Kaniko + Harbor.
## Build from repository root:
##   docker build -f deploy/Dockerfile -t go-vercel-orchestrator:latest .
## Run:
##   docker run -p 8081:8081 --env-file deploy/vars.env go-vercel-orchestrator:latest

FROM golang:1.25-alpine AS build

RUN apk add --no-cache ca-certificates git tzdata
ENV GOTOOLCHAIN=auto
WORKDIR /src

COPY deploy/go.mod deploy/go.sum ./
RUN go mod download

COPY deploy/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o /out/orchestrator .

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/orchestrator /app/orchestrator

EXPOSE 8081
ENV ADDR=":8081"

CMD ["/app/orchestrator"]
