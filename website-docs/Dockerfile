# Build from the repository root with:
#   docker build -t weknora-site website-docs
# No host Node.js installation or prebuilt static-site/ is required.
FROM node:24-bookworm-slim AS builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
ENV NEXT_TELEMETRY_DISABLED=1

# Install both locked dependency sets before copying source for layer caching.
COPY package.json package-lock.json ./
COPY homepage/package.json homepage/package-lock.json ./homepage/
RUN npm run setup

COPY . .
RUN npm run build

# Pin the runtime base. Do NOT switch back to floating `nginx:stable-alpine`:
# that tag is now Alpine 3.24+, which fails to start on intranet hosts with
# CentOS 7's kernel 3.10 and old libseccomp (the v0.7.0 frontend outage).
# This digest is nginx 1.30.3 / Alpine 3.23.5, the same base as the frontend
# image that still starts there. apk stays on the 3.23 repos.
FROM nginx:1.30.3-alpine@sha256:0d3b80406a13a767339fbe2f41406d6c7da727ab89cf8fae399e81f780f814d1 AS runtime
# Some deployment platforms wrap the startup command with /bin/bash -c.
RUN apk add --no-cache bash
ENV WEBSITE_NGINX_PORT=80
# Keep a default config alongside the template, as in the internal frontend image.
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
RUN mkdir -p /etc/nginx/templates \
    && sed 's/listen 80;/listen ${WEBSITE_NGINX_PORT};/' /etc/nginx/conf.d/default.conf \
      > /etc/nginx/templates/default.conf.template \
    && grep -q 'listen ${WEBSITE_NGINX_PORT};' /etc/nginx/templates/default.conf.template \
    && rm -rf /usr/share/nginx/html/*
COPY --from=builder /build/static-site/ /usr/share/nginx/html/
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
EXPOSE 80
ENTRYPOINT ["/docker-entrypoint.sh"]
