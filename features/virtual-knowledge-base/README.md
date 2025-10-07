# Virtual Knowledge Base Feature Bundle

This bundle introduces a virtual knowledge base subsystem for WeKnora that enables user-defined document tagging, dynamic virtual knowledge base construction, and enhanced search experiences without modifying the existing WeKnora codebase.

> **Important:** All files under `features/virtual-knowledge-base/` are self-contained and are intended to be submitted as a standalone pull request. Integrators can review, merge, and selectively wire these components into the core application as needed.

## Contents

- Architecture and design documentation
- Backend Go modules (models, repositories, services, handlers)
- PostgreSQL migration scripts
- Vue 3 / Vite frontend examples (aligned with main project stack)
- API specifications and Postman collections
- End-to-end, integration, and unit test stubs
- Deployment scripts and Docker assets
- Examples and integration guides

## Design Overview

- **Purpose**: Extend WeKnora with a pluggable virtual knowledge base (VKB) subsystem that introduces tag-driven document organization without touching the core application.
- **Scope**: Provides Go services, PostgreSQL migrations, and a Vue 3 frontend bundle that can run standalone or be merged into the primary console when needed.
- **Integration Strategy**: Ship as an isolated feature pack (`features/virtual-knowledge-base/`) so reviewers can evaluate, cherry-pick, or iteratively integrate components.

## Feature Highlights

- **Tag Taxonomy Management**: Define tag categories, create tags with weights, and assign tags to documents via REST APIs exposed under `/api/v1/virtual-kb` (`internal/`, `web/src/api/tag.ts`).
- **Virtual Knowledge Base Builder**: Compose VKB instances using tag filters, boolean operators, and weights; manage instances through `VirtualKBManagement.vue` with `VirtualKBList.vue` + `VirtualKBEditor.vue`.
- **Enhanced Search Pipeline**: Execute searches scoped by VKB filters or ad-hoc tag conditions; frontend surface provided in `EnhancedSearch.vue`, backend hooks outlined in `internal/service/impl/virtual_kb_service.go`.
- **Extensible Frontend Shell**: Vue router factory (`web/src/router/index.ts`) and Pinia-ready structure enable easy embedding into the main console while keeping the demo runnable in isolation.

## Quick Start

```bash
# 1. Load environment variables
cp .env.example .env

# 2. Review feature configuration
cat config/virtual-kb.yaml

# 3. Run database migrations
./scripts/migrate.sh

# 4. Build backend and frontend assets
./scripts/build.sh

# 5. Execute tests (unit + integration stubs)
./scripts/test.sh

# 6. Launch the feature stack (standalone mode)
docker compose -f docker/docker-compose.virtual-kb.yml up --build
```

### Frontend (Vue) Quick Start

```bash
cd web

# Install dependencies (sub-project only, does not affect main project)
npm install

# Local development
npm run dev

# Run type checking
npm run type-check

# Build static assets (output directory: web/dist/)
npm run build

# Preview build artifacts
npm run preview
```

## Integration Overview

1. Review architectural notes in `DESIGN.md`.
2. Apply migrations located in `migrations/` to your PostgreSQL instance.
3. Register backend handlers via adapters (see backend integration examples).
4. Vue sub-project is located in `web/`. Run `npm install && npm run build` to generate `web/dist/`, which can be embedded into existing Vue applications as needed.
5. Follow deployment notes in this README or backend documentation.

### Routing & Store Integration Notes

- This sub-project provides `src/router/index.ts` as example route mapping for demo/preview purposes only. When migrating pages to the main project, you can directly import `TagManagement.vue`, `VirtualKBManagement.vue`, `EnhancedSearch.vue` and mount them to existing routes.
- For global state management, refer to the main project's Pinia structure. You can extend modules in `src/stores/` or reuse existing stores.
- HTTP requests are uniformly encapsulated in `src/api/`, defaulting to `/api/v1/virtual-kb`. You can adjust the baseURL or interceptors as needed during integration.

## Support

For questions related to this feature bundle, refer to:

- Backend API documentation in `internal/` modules
- Database schema in `migrations/`
- Frontend component examples in `web/src/`

This feature pack is designed for evaluation and iterative integration. Adjust and extend as required to fit your production environment.
