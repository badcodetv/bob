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

FROM gcr.io/distroless/static-debian12
COPY --from=api /bob /bob
COPY --from=web /web/dist /web
ENV BOB_WEB_DIR=/web BOB_ADDR=:8090
EXPOSE 8090
# Root in the container: Bob talks to the host Docker socket, which is root-owned.
USER 0
ENTRYPOINT ["/bob"]
