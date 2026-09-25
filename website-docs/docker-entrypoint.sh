#!/bin/sh
set -eu

export WEBSITE_NGINX_PORT="${WEBSITE_NGINX_PORT:-80}"

# Substitute only the port; preserve Nginx variables such as $uri.
envsubst '${WEBSITE_NGINX_PORT}' \
  < /etc/nginx/templates/default.conf.template \
  > /etc/nginx/conf.d/default.conf

# Match the frontend entrypoint: configure and start Nginx even with no arguments.
exec nginx -g 'daemon off;'
