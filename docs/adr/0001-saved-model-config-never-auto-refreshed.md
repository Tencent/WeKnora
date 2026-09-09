# Saved model configuration is never auto-refreshed from the catalog

When a model record is saved, its stored parameter values become final: catalog
(models.json / remote metadata) prefill applies only to unsaved forms, never as
an "update" to existing records. We chose this over field-level provenance
tracking because provenance would have to ship with the storage schema from day
one — retrofitting it later is impossible once saved values cannot be
distinguished from user-typed ones. Accepted cost: a stale context window saved
in September stays stale even after the catalog corrects it in November; the
user must edit it manually.

Open edge (default assumed): changing the model ID inside the edit dialog counts
as "new selection" and re-triggers prefill over the stored values.
