# Production image. Build static assets on the host first:
#   ./scripts/build_frontend_dist.sh
#
# Base image pinned by digest for reproducibility. Do NOT switch back to the
# floating `nginx:stable-alpine` tag: it drifts with every stable release and a
# newer Alpine base (3.24+) fails to start on old hosts such as CentOS 7
# (kernel 3.10 + old libseccomp), which broke v0.7.0. This digest is
# nginx 1.30.3 / Alpine 3.23.5, the same base as the working v0.6.3 image.
FROM nginx:1.30.3-alpine@sha256:0d3b80406a13a767339fbe2f41406d6c7da727ab89cf8fae399e81f780f814d1

COPY dist /usr/share/nginx/html

COPY nginx.conf /etc/nginx/templates/default.conf.template

COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENV MAX_FILE_SIZE_MB=50

EXPOSE 80

ENTRYPOINT ["/docker-entrypoint.sh"]
