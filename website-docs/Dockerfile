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

FROM nginx:stable-alpine AS runtime
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
