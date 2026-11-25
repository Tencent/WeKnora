# Subpath Deployment

WeKnora can be built for deployment under a URL prefix such as `https://example.com/weknora/`.

This setup assumes the frontend and API share a public origin and an external reverse proxy strips the prefix before forwarding requests to WeKnora's internal Nginx server.

## Configuration

Set the frontend base path at build time:

```bash
docker build \
  --build-arg VITE_BASE_PATH=/weknora/ \
  -t weknora-ui:subpath \
  frontend/
```

For a local frontend build:

```bash
cd frontend
VITE_BASE_PATH=/weknora/ npm run build
```

Configure the backend's externally visible frontend URL:

```dotenv
FRONTEND_BASE_URL=https://example.com/weknora
```

The external OIDC callback URL should be registered with the identity provider as:

```text
https://example.com/weknora/api/v1/auth/oidc/callback
```

## Docker Compose (built-in prefix)

When deploying with the shipped `docker-compose.yml`, set one variable
instead of configuring an external gateway:

```dotenv
APP_BASE_PATH=/weknora
```

Then rebuild the frontend image (the base path is baked in at build time)
and restart both services:

```bash
docker compose build frontend
docker compose up -d app frontend
```

With `APP_BASE_PATH` set, the internal Nginx routes `/weknora/*` (static
files, `/api/`, `/mcp/`, `/r/`, `/embed/`, `/files/`) and the backend
strips the prefix before routing, so no external gateway stripping is
needed. The external-gateway setup below remains supported: a gateway that
strips the prefix also works, because root paths pass through unchanged.

Absolute URLs that leave WeKnora still need the prefix in their own
settings: `FRONTEND_BASE_URL=https://example.com/weknora` for invite
links, and `APP_EXTERNAL_URL=https://example.com/weknora` when IM image
links are routed through `/r/`.

## Reverse Proxy

The external gateway should remove `/weknora/` before forwarding requests to the internal frontend Nginx server.

Example Nginx configuration, inside the `http` context:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' '';
}

server {
    # Configure your HTTPS listener and certificates.

    location = /weknora {
        return 308 /weknora/;
    }

    location ^~ /weknora/ {
        proxy_pass http://weknora-frontend:80/;

        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        proxy_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

Replace `weknora-frontend:80` with the internal frontend service address.

The trailing slash in `proxy_pass` is important. It ensures `/weknora/api/v1/...` is forwarded internally as `/api/v1/...`.

The internal frontend Nginx configuration continues to serve files and proxy APIs at their original root-relative paths.

## Compatibility and Limitations

- Root-path deployments remain the default when `VITE_BASE_PATH` is unset.
- Changing `VITE_BASE_PATH` requires rebuilding the frontend assets or image.
- This configuration does not provide runtime-switchable base paths.
- MCP OAuth redirects, protected file images and embed snippet URLs follow
  the configured base path. The commented-out dedicated embed-subdomain
  Nginx block in `frontend/nginx.conf` is not prefix-aware, and the Helm
  chart does not wire `APP_BASE_PATH` yet.
- Auxiliary integrations and complete embedded-page workflows require separate validation.
- Full OIDC login with a live identity provider should be verified before production rollout.