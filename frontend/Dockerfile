# Production image. Build static assets on the host first:
#   ./scripts/build_frontend_dist.sh
FROM nginx:stable-alpine

COPY dist /usr/share/nginx/html

COPY nginx.conf /etc/nginx/templates/default.conf.template

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENV MAX_FILE_SIZE_MB=50

EXPOSE 80

ENTRYPOINT ["/docker-entrypoint.sh"]
