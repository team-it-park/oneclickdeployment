## Orchestrator API (Echo) that builds/deploys user apps via Kubernetes + Kaniko + Harbor.
## Build with: docker build -t go-vercel-orchestrator:latest .
## Run with:   docker run -p 8081:8081 --env-file vars.env go-vercel-orchestrator:latest

FROM golang:1.25-alpine AS build

RUN apk add --no-cache ca-certificates git tzdata
WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o /out/orchestrator ./main.go

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/orchestrator /app/orchestrator

EXPOSE 8081
ENV ADDR=":8081"

CMD ["/app/orchestrator"]

