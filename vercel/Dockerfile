## Vercel web app (Echo) + static UI in /public
## Build from repository root:
##   docker build -f vercel/Dockerfile -t go-vercel-web:latest .
## Run:
##   docker run -p 8080:8080 --env-file vercel/vars.env go-vercel-web:latest

FROM golang:1.22-alpine AS build

RUN apk add --no-cache ca-certificates git tzdata
WORKDIR /src

COPY vercel/go.mod vercel/go.sum ./
RUN go mod download

COPY vercel/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags="-s -w" -o /out/vercel ./main.go

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/vercel /app/vercel
COPY --from=build /src/public /app/public
COPY --from=build /src/db.sql /app/db.sql

EXPOSE 8080
ENV ADDR=":8080"

CMD ["/app/vercel"]
