# Optional runtime operations overview

Settings → System Administration → Task Queues now includes an operations overview.
The existing SystemAdmin endpoint reports IM active workers, queue depth, configured
local capacity, and rejection/timeout counters. These are current-process metrics,
independent of the Redis global IM limit and provider-account concurrency.
Disabled IM queues show unknown values.

Host metrics come from an optional external JSON file. Without a collector, the
IM card still works and host values remain unknown. Samples older than 10 minutes
are visibly stale. The backend accepts regular files up to 128 KiB, rejects invalid
types/timestamps, and never invokes Docker or systemd.

## Collector (Linux, Python 3 standard library)

```sh
python3 scripts/monitoring/collect-status.py \
  --output /var/lib/weknora/monitoring/status.json \
  --disk / --disk /srv/weknora \
  --container WeKnora-app \
  --backup WeKnora=/srv/backups/weknora \
  --service weknora-backup.service \
  --running-service weknora.service
```

Selection options are repeatable and optional. By default only memory, CPU, load,
and the root filesystem are sampled. Schedule the command every two minutes with
your host scheduler. Docker/log access is needed only by the collector. Container
failures are isolated; rotated local logs are included in byte totals. Remote log
drivers can have unknown size/retention. Only metadata and byte counts are exported;
no environment variables, log text, or backup contents are returned. The collector
does not change retention, restart services, or perform backups.

The optional backup convention is `DIRECTORY/latest` pointing to a direct snapshot
child containing `COMPLETE`. Its mtime is the completion time. Missing, incomplete,
outside-directory, and future-dated markers remain unknown. Completion older than
26 hours is overdue. Snapshot count is a directory count and does not verify a
backup or restore. `--service` allows an inactive successful scheduled job;
`--running-service` also requires an active service. Missing units remain unknown.

Mount the directory read-only (a single-file mount retains the old inode after
atomic sample replacement):

```yaml
services:
  app:
    environment:
      WEKNORA_OPERATIONS_STATUS_FILE: /run/weknora-monitoring/status.json
    volumes:
      - /var/lib/weknora/monitoring:/run/weknora-monitoring:ro
```

For native deployments, set the environment variable to the readable JSON path.
Protect the directory from untrusted writes. In multi-instance setups, mount the
intended host sample for each instance. Host freshness is independent of live IM
numbers. Removing the environment variable disables host telemetry.

## Verification

```sh
python3 -m unittest discover -s scripts/monitoring -p 'test_*.py'
go test -race ./internal/im ./internal/handler ./internal/router -run 'Runtime|Operations' -count=1
cd frontend && npm run type-check && npm run check-i18n
```

Check normal/stale/missing samples and disabled IM as SystemAdmin. Ordinary members
and anonymous requests retain the existing 403/401 behavior. No scheduling changes,
schema migrations, or new production dependencies are introduced.
