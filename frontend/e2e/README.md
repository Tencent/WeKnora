# noVNC regression

Apply shared infrastructure PR #4141, then run `npm run test:e2e -- e2e/novnc.spec.ts`. The regression emulates a never-resolving VideoDecoder capability probe: upstream's static noVNC import blocks chat startup, while the lazy import leaves chat available. The browser harness and CI proposal are reviewed separately in #4141.
