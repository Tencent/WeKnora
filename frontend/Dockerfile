# Production image — two-stage build (no host-side npm required).
# Mirrors scripts/build_frontend_dist.sh:
#   npm ci + VITE_IS_DOCKER=true + VITE_FRONTEND_COMMIT
#
# Dist is architecture-independent. The builder uses $BUILDPLATFORM so
# multi-arch CI (linux/amd64,linux/arm64) compiles Vite once on the runner
# instead of qemu-emulating Node, and both runtime images share the same dist.
#
#   docker build --platform linux/amd64 \
#     --build-arg VITE_FRONTEND_COMMIT=$(git rev-parse --short HEAD) \
#     -t wechatopenai/weknora-ui:latest \
#     frontend/
#
# Optional npm mirror (e.g. corporate registry):
#   --build-arg NPM_REGISTRY=https://registry.npmmirror.com
#
# Docker Desktop with a small VM:
#   --build-arg NODE_MAX_OLD_SPACE_SIZE=2048

# ---------------------------------------------------------------------------
# Stage 1: build static assets
# ---------------------------------------------------------------------------
# Pin by digest: compiled JS is shipped in the runtime image, so a floating
# node tag could change production assets. This index includes linux/amd64
# and linux/arm64 (Node 24 / Debian bookworm slim).
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS builder

WORKDIR /frontend

ARG NPM_REGISTRY=
ARG NODE_MAX_OLD_SPACE_SIZE=4096

ENV VITE_IS_DOCKER=true
# Vite production builds of this SPA routinely exceed Node's default heap.
ENV NODE_OPTIONS="--max-old-space-size=${NODE_MAX_OLD_SPACE_SIZE}"

# package-lock references file:packages/xlsx-*.tgz — copy before npm ci.
COPY package.json package-lock.json ./
COPY packages ./packages
RUN if [ -n "$NPM_REGISTRY" ]; then \
      npm ci --registry="$NPM_REGISTRY"; \
    else \
      npm ci; \
    fi

COPY . .
# After npm ci so a new commit hash does not bust the dependency layer.
ARG VITE_FRONTEND_COMMIT=unknown
ENV VITE_FRONTEND_COMMIT=${VITE_FRONTEND_COMMIT}
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: nginx runtime
# ---------------------------------------------------------------------------
# Base image pinned by digest for reproducibility. Do NOT switch back to the
# floating `nginx:stable-alpine` tag: it drifts with every stable release and a
# newer Alpine base (3.24+) fails to start on old hosts such as CentOS 7
# (kernel 3.10 + old libseccomp), which broke v0.7.0. This digest is
# nginx 1.30.3 / Alpine 3.23.5, the same base as the working v0.6.3 image.
FROM nginx:1.30.3-alpine@sha256:0d3b80406a13a767339fbe2f41406d6c7da727ab89cf8fae399e81f780f814d1

COPY --from=builder /frontend/dist /usr/share/nginx/html

COPY nginx.conf /etc/nginx/templates/default.conf.template

# Copied outside templates/ on purpose: envsubst would eat $host and the other
# nginx variables in it.
COPY nginx-api-proxy.conf /etc/nginx/api-proxy.conf

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENV MAX_FILE_SIZE_MB=50

EXPOSE 80

ENTRYPOINT ["/docker-entrypoint.sh"]
