FROM nginx:stable-alpine
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY static-site/ /usr/share/nginx/html/
EXPOSE 80
