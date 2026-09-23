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
# The official image renders /etc/nginx/templates/*.template with envsubst at
# startup, so `docker run -e WEBSITE_NGINX_PORT=8080` changes the listen port without a
# rebuild. deploy/nginx.conf stays a plain config for static deployments.
ENV WEBSITE_NGINX_PORT=80
COPY deploy/nginx.conf /etc/nginx/templates/default.conf.template
RUN sed -i 's/listen 80;/listen ${WEBSITE_NGINX_PORT};/' /etc/nginx/templates/default.conf.template \
    && grep -q 'listen ${WEBSITE_NGINX_PORT};' /etc/nginx/templates/default.conf.template \
    && rm -rf /etc/nginx/conf.d/default.conf /usr/share/nginx/html/*
COPY --from=builder /build/static-site/ /usr/share/nginx/html/
EXPOSE 80
