## Vercel web app (Echo) + static UI in /public
## Build with: docker build -t go-vercel-web:latest .
## Run with:   docker run -p 8080:8080 --env-file vars.env go-vercel-web:latest

FROM golang:1.22-alpine AS build

RUN apk add --no-cache ca-certificates git tzdata
WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o /out/vercel ./main.go

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/vercel /app/vercel
COPY --from=build /src/public /app/public

EXPOSE 8080
ENV ADDR=":8080"

CMD ["/app/vercel"]

