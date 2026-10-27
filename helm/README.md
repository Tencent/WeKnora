# WeKnora Helm Chart

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/weknora)](https://artifacthub.io/packages/helm/weknora/weknora)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)

Helm chart for deploying [WeKnora](https://github.com/Tencent/WeKnora) - an AI-powered Knowledge RAG Platform.

## Overview

WeKnora is an intelligent knowledge base platform that combines:
- Document parsing and understanding
- Vector search with BM25 hybrid retrieval
- LLM integration for conversational AI
- Multi-tenant support with encryption

## Prerequisites

- Kubernetes 1.25+
- Helm 3.10+
- PV provisioner support in the underlying infrastructure
- Ingress controller (nginx-ingress recommended) for external access

## Quick Start

```bash
# Add required secrets
helm install weknora ./helm \
  --namespace weknora \
  --create-namespace \
  --set secrets.dbPassword=<your-db-password> \
  --set secrets.redisPassword=<your-redis-password> \
  --set secrets.jwtSecret=<your-jwt-secret>
```

## Architecture

```
                    ┌─────────────┐
                    │   Ingress   │
                    └──────┬──────┘
                           │
           ┌───────────────┴───────────────┐
           │                               │
           ▼                               ▼
    ┌─────────────┐                 ┌─────────────┐
    │  Frontend   │                 │   Backend   │
    │  (Vue.js)   │                 │   (Go/Gin)  │
    └─────────────┘                 └──────┬──────┘
                                           │
                    ┌──────────────────────┼──────────────────────┐
                    │                      │                      │
                    ▼                      ▼                      ▼
             ┌─────────────┐        ┌─────────────┐        ┌─────────────┐
             │  Docreader  │        │  PostgreSQL │        │    Redis    │
             │   (gRPC)    │        │  (ParadeDB) │        │   (Queue)   │
             └─────────────┘        └─────────────┘        └─────────────┘
```

## Installation

### Basic Installation

```bash
helm install weknora ./helm \
  --namespace weknora \
  --create-namespace \
  --set secrets.dbPassword=secure-password \
  --set secrets.redisPassword=secure-password \
  --set secrets.jwtSecret=$(openssl rand -base64 32)
```

### With Ingress

```bash
helm install weknora ./helm \
  --namespace weknora \
  --create-namespace \
  --set ingress.enabled=true \
  --set ingress.host=weknora.example.com \
  --set ingress.tls.enabled=true \
  --set ingress.tls.secretName=weknora-tls \
  --set secrets.dbPassword=secure-password \
  --set secrets.redisPassword=secure-password \
  --set secrets.jwtSecret=$(openssl rand -base64 32)
```

### With External LLM (Ollama)

```bash
helm install weknora ./helm \
  --namespace weknora \
  --create-namespace \
  --set app.extraEnv[0].name=OLLAMA_BASE_URL \
  --set app.extraEnv[0].value=http://ollama.ollama:11434 \
  --set app.extraEnv[1].name=INIT_LLM_MODEL_NAME \
  --set app.extraEnv[1].value=qwen2.5:7b \
  --set secrets.dbPassword=secure-password \
  --set secrets.redisPassword=secure-password \
  --set secrets.jwtSecret=$(openssl rand -base64 32)
```

### Production Installation

For production with the default local file storage, use a single App replica and
an update strategy that releases the old Pod's volume before starting its
replacement:

```yaml
# values-production.yaml
global:
  storageClass: "fast-ssd"

app:
  replicaCount: 1
  strategy:
    type: Recreate
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: 2
      memory: 4Gi

postgresql:
  persistence:
    size: 100Gi

ingress:
  enabled: true
  host: weknora.company.com
  tls:
    enabled: true
    secretName: weknora-tls

secrets:
  existingSecret: weknora-secrets  # Use pre-created secret
```

```bash
helm install weknora ./helm \
  --namespace weknora \
  --create-namespace \
  -f values-production.yaml
```

The chart-created data-files PVC uses `ReadWriteOnce` (RWO). RWO allows several
Pods to mount the volume on the same node; it does not provide shared writable
access across nodes. When `app.strategy.type` is omitted, the chart detects this
single-writer storage and defaults to `Recreate`, which terminates the old Pods
before starting replacements. **App upgrades have downtime**; volume
detach/attach can still take time. This strategy does not make RWO storage
suitable for multiple App replicas across nodes. See Kubernetes documentation on
[access modes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes)
and [Deployment strategies](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#strategy).

For multiple App replicas using local storage, first provision and verify a
shared filesystem PVC that supports `ReadWriteMany` (RWX) on the intended nodes.
Apply the following override together with `values-production.yaml`:

```yaml
# values-shared-storage.yaml
app:
  replicaCount: 3
  strategy:
    type: RollingUpdate

dataFiles:
  persistence:
    enabled: true
    existingClaim: weknora-shared-files  # Pre-provisioned, verified RWX PVC
```

```bash
helm install weknora ./helm \
  --namespace weknora \
  --create-namespace \
  -f values-production.yaml \
  -f values-shared-storage.yaml
```

The existing claim must be in the release namespace and allow the App containers
to read and write `/data/files`. The chart references it without creating or
validating its access mode. All App replicas must see the same files; disabling
persistence or assigning independent local volumes does not provide shared
storage. This override addresses App file storage only, not high availability
for the chart's database or other components. Object storage is another option
only after configuring the provider and checking that all paths needed by your
deployment are accessible to every replica.

### Mounting Extra Volumes into the App Pod

The chart mounts a single `data-files` volume by default. Use
`app.extraVolumes` and `app.extraVolumeMounts` to attach additional ConfigMaps,
Secrets, PVCs, `emptyDir` or any other volume type to the backend container —
for example a CA bundle, a config file, or a credentials Secret:

```yaml
app:
  extraVolumes:
    - name: ca-bundle
      configMap:
        name: my-ca-bundle
    - name: credentials
      secret:
        secretName: my-credentials
  extraVolumeMounts:
    - name: ca-bundle
      mountPath: /etc/ssl/certs/custom
      readOnly: true
    - name: credentials
      mountPath: /etc/credentials
      readOnly: true
```

Both lists are rendered verbatim, so any field Kubernetes accepts in
`spec.template.spec.volumes` and in a container's `volumeMounts` may be used.

**Requirements and caveats:**

- The referenced ConfigMap, Secret or PVC must already exist in the namespace.
  The chart does not create them.
- Each `name` in `extraVolumeMounts` must match a `name` in `extraVolumes`;
  otherwise the API server rejects the Pod.
- `dataFiles.persistence.enabled` still governs only the built-in `data-files`
  volume. Extra volumes are unaffected by it.
- Verify the rendering before applying:
  `helm template weknora ./helm -f values-production.yaml | grep -A5 extraVolume`

## Configuration

### Service names

Optional Service name overrides preserve the following defaults:

| Parameter | Default when omitted or empty |
|-----------|-------------------------------|
| `app.service.name` | `app` |
| `frontend.service.name` | `frontend` |
| `postgresql.service.name` | `postgres` |
| `redis.service.name` | `redis` |
| `docreader.service.name` | `docreader` |
| `neo4j.service.name` | `neo4j` (when enabled) |

For example, save this as `service-names.yaml`:

```yaml
app:        {service: {name: weknora-app}}
frontend:   {service: {name: weknora-frontend}}
postgresql: {service: {name: weknora-postgres}}
redis:      {service: {name: weknora-redis}}
docreader:  {service: {name: weknora-docreader}}
neo4j:      {service: {name: weknora-neo4j}}
```

Pass `-f service-names.yaml` alongside your other values. Names must be valid
Kubernetes Service names. Services, backend addresses, Ingress and installation
notes use these names consistently. Frontend's `APP_HOST` follows the App Service
unless `frontend.appHost` is explicitly set.

For multiple releases in one namespace, use distinct names for every enabled
Service (for example, `alpha-app` and `beta-app`). Defaults are not release-prefixed
and still collide. Configure separate Ingress hosts when needed.

Omitted or empty overrides preserve Service names on upgrade. Changing a name on
an existing installation requires a DNS/consumer cutover plan; matching a
pre-existing Service name does not automatically transfer its ownership to Helm.

### Global Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `global.storageClass` | Storage class for PVCs | `""` |
| `global.imagePullSecrets` | Image pull secrets | `[]` |
| `global.maxFileSizeMB` | Maximum upload size in MB (frontend, app, docreader) | `50` |
| `global.podSecurityContext` | Pod security context | See values.yaml |
| `global.containerSecurityContext` | Container security context | See values.yaml |

### ServiceAccount

| Parameter | Description | Default |
|-----------|-------------|---------|
| `serviceAccount.create` | Create ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount name | `""` |
| `serviceAccount.annotations` | ServiceAccount annotations | `{}` |

### App (Backend)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `app.enabled` | Enable backend | `true` |
| `app.replicaCount` | Number of replicas | `1` |
| `app.strategy.type` | App update strategy: `RollingUpdate` or `Recreate`; empty selects `Recreate` for persistent non-RWX data-files storage and `RollingUpdate` otherwise | `""` |
| `app.strategy.rollingUpdate.maxSurge` | Extra Pods during rolling updates (integer or percentage); ignored with `Recreate` | `1` |
| `app.strategy.rollingUpdate.maxUnavailable` | Unavailable Pods during rolling updates (integer or percentage); ignored with `Recreate` | `0` |
| `app.image.repository` | Image repository | `wechatopenai/weknora-app` |
| `app.image.tag` | Image tag | `""` (uses appVersion) |
| `app.resources` | Resource limits | See values.yaml |
| `app.env` | Environment variables | See values.yaml |
| `app.extraEnv` | Additional env vars | `[]` |
| `app.extraVolumes` | Additional Pod volumes (rendered verbatim into `spec.template.spec.volumes`) | `[]` |
| `app.extraVolumeMounts` | Additional volume mounts for the app container (names must match `extraVolumes`) | `[]` |

### Data Files Storage

| Parameter | Description | Default |
|-----------|-------------|---------|
| `dataFiles.persistence.enabled` | Create or mount persistent uploaded-file storage | `true` |
| `dataFiles.persistence.accessModes` | Access modes for a chart-created data-files PVC; `ReadWriteMany` permits rolling App updates | `[ReadWriteOnce]` |
| `dataFiles.persistence.existingClaim` | Use a pre-provisioned PVC instead of creating one | `""` |

### Frontend

| Parameter | Description | Default |
|-----------|-------------|---------|
| `frontend.enabled` | Enable frontend | `true` |
| `frontend.replicaCount` | Number of replicas | `1` |
| `frontend.image.repository` | Image repository | `wechatopenai/weknora-ui` |
| `frontend.image.tag` | Image tag | `""` (uses appVersion) |

### DocReader

| Parameter | Description | Default |
|-----------|-------------|---------|
| `docreader.enabled` | Enable DocReader | `true` |
| `docreader.replicaCount` | Number of replicas | `1` |
| `docreader.image.repository` | Image repository | `wechatopenai/weknora-docreader` |
| `docreader.image.tag` | Image tag | `""` (uses appVersion) |

### PostgreSQL (ParadeDB)

| Parameter | Description | Default |
|-----------|-------------|---------|
| `postgresql.enabled` | Enable PostgreSQL | `true` |
| `postgresql.image.repository` | Image repository | `paradedb/paradedb` |
| `postgresql.image.tag` | Image tag | `v0.18.9-pg17` |
| `postgresql.persistence.enabled` | Enable persistence | `true` |
| `postgresql.persistence.size` | PVC size | `10Gi` |

### Redis

| Parameter | Description | Default |
|-----------|-------------|---------|
| `redis.enabled` | Enable Redis | `true` |
| `redis.image.repository` | Image repository | `redis` |
| `redis.image.tag` | Image tag | `7-alpine` |
| `redis.persistence.enabled` | Enable persistence | `true` |
| `redis.persistence.size` | PVC size | `1Gi` |

### Ingress

| Parameter | Description | Default |
|-----------|-------------|---------|
| `ingress.enabled` | Enable ingress | `false` |
| `ingress.className` | Ingress class | `nginx` |
| `ingress.host` | Hostname | `weknora.example.com` |
| `ingress.tls.enabled` | Enable TLS | `false` |
| `ingress.tls.secretName` | TLS secret name | `""` |

### Secrets

| Parameter | Description | Default |
|-----------|-------------|---------|
| `secrets.dbUser` | Database username | `postgres` |
| `secrets.dbPassword` | Database password | `""` (required) |
| `secrets.dbName` | Database name | `weknora` |
| `secrets.redisPassword` | Redis password | `""` (required) |
| `secrets.jwtSecret` | JWT signing secret | `""` (required) |
| `secrets.existingSecret` | Use existing secret | `""` |

### Optional Components

These map to docker-compose profiles:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `minio.enabled` | Enable MinIO storage | `false` |
| `neo4j.enabled` | Enable Neo4j (GraphRAG) | `false` |
| `qdrant.enabled` | Enable Qdrant vector DB | `false` |

## Security Best Practices

### Secret Management

**Never commit secrets to Git!** Use one of these approaches:

1. **Helm --set flags** (for testing)
   ```bash
   helm install weknora ./helm --set secrets.dbPassword=xxx
   ```

2. **External Secrets Operator** (recommended for production)
   ```yaml
   secrets:
     existingSecret: weknora-external-secret
   ```

3. **Sealed Secrets** (for GitOps)
   ```bash
   kubeseal < secret.yaml > sealed-secret.yaml
   ```

### Pod Security

The chart follows CNCF security best practices:
- Runs as non-root user
- Read-only root filesystem where possible
- Drops all capabilities
- Uses seccomp profiles

## Upgrading

```bash
helm upgrade weknora ./helm \
  --namespace weknora \
  --reuse-values
```

## Uninstalling

```bash
helm uninstall weknora --namespace weknora

# Optional: Remove PVCs
kubectl delete pvc -n weknora -l app.kubernetes.io/instance=weknora
```

## Troubleshooting

### Check Pod Status
```bash
kubectl get pods -n weknora
```

### View Logs
```bash
# Backend logs
kubectl logs -n weknora -l app.kubernetes.io/component=app -f

# Frontend logs
kubectl logs -n weknora -l app.kubernetes.io/component=frontend -f
```

### Common Issues

**Pod stuck in Pending**
- Check if PVCs are bound: `kubectl get pvc -n weknora`
- Verify storage class exists: `kubectl get sc`

**Connection refused errors**
- Wait for all pods to be Ready
- Check service endpoints: `kubectl get endpoints -n weknora`

**Database connection errors**
- Verify secrets are correct
- Check PostgreSQL logs: `kubectl logs -n weknora -l app.kubernetes.io/component=database`

## Contributing

See [CONTRIBUTING.md](https://github.com/Tencent/WeKnora/blob/main/CONTRIBUTING.md) in the main repository.

## References

This Helm chart follows best practices from:
- [Helm Best Practices](https://helm.sh/docs/chart_best_practices/)
- [ArgoCD Helm Chart](https://github.com/argoproj/argo-helm)
- [Prometheus Helm Charts](https://github.com/prometheus-community/helm-charts)
- [cert-manager Helm Chart](https://github.com/cert-manager/cert-manager)

## License

This chart is licensed under the MIT License - see the [LICENSE](https://github.com/Tencent/WeKnora/blob/main/LICENSE) file for details.
