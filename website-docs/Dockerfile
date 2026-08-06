# Build from repository root:
#   docker build -f website-docs/Dockerfile -t wechatopenai/weknora-docs:latest .
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

# Same pinned nginx base as frontend/Dockerfile (CentOS 7 / old libseccomp compatible).
FROM nginx:1.30.3-alpine@sha256:0d3b80406a13a767339fbe2f41406d6c7da727ab89cf8fae399e81f780f814d1

COPY --from=builder /build/website-docs/.vitepress/dist /usr/share/nginx/html
COPY website-docs/nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
