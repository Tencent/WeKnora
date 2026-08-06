# Build from repository root:
#   docker build --platform linux/amd64 -f website-docs/Dockerfile -t wechatopenai/weknora-docs:latest .
#
# Gateway (preserve /docs/ prefix):
#   location /docs/ { proxy_pass http://weknora-docs:80/docs/; }
#
# VitePress reads VERSION from the repo root (see .vitepress/version.ts), so the
# builder stage mirrors the monorepo layout instead of flattening website-docs/.
FROM node:22-bookworm-slim AS builder

RUN apt-get update \
    && apt-get install -y --no-install-recommends git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

COPY VERSION VERSION
COPY website-docs/package.json website-docs/package-lock.json website-docs/
RUN cd website-docs && npm ci

COPY website-docs/ website-docs/
RUN cd website-docs && npm run build

# Align with internal frontend image: stable-alpine + custom entrypoint + bash.
FROM nginx:stable-alpine AS production-stage

COPY --from=builder /build/website-docs/.vitepress/dist /usr/share/nginx/html

RUN apk add --no-cache bash

COPY website-docs/nginx.conf /etc/nginx/conf.d/default.conf
COPY website-docs/docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

EXPOSE 80

ENTRYPOINT ["/docker-entrypoint.sh"]
