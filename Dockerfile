# bob: the API and the built web app, one image. Project containers use runtime/Dockerfile.
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.25-bookworm AS api
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bob ./cmd/bob

# nonroot: Bob serves HTTP and talks to Postgres and the project containers. It has no Docker
# socket to reach — each project's container is declared in the compose file, not started here.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /bob /bob
COPY --from=web /web/dist /web
ENV BOB_WEB_DIR=/web BOB_ADDR=:8070
EXPOSE 8070
ENTRYPOINT ["/bob"]
