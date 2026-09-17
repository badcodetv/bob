# bob-runtime: the base image every project container runs. Project images are
# `FROM bob-runtime` and only install software — never change the entrypoint.
FROM node:22-bookworm-slim AS build
WORKDIR /app
COPY package.json package-lock.json tsconfig.json ./
RUN npm ci
COPY src ./src
RUN npx tsc -p . && npm prune --omit=dev

FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates ripgrep procps \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json entrypoint.sh sync.sh ./
RUN mkdir -p /project && chown node:node /project
ENV BOB_PROJECT_DIR=/project \
    CLAUDE_CONFIG_DIR=/project/.bob/claude \
    CODEX_HOME=/project/.bob/codex \
    XDG_DATA_HOME=/project/.bob/opencode \
    PORT=8080
USER node
EXPOSE 8080
ENTRYPOINT ["/bin/sh", "/app/entrypoint.sh"]
