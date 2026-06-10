# Changelog

All notable changes to this project will be documented in this file.

The format is based on ["Keep a Changelog"](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.26.0] - 2026-02-03

### Upgrades
- Prepare CI for Go 1.24.x and 1.25.x (commit: e9037f0)

### Added
- Add Headers support to tasks (PR: https://github.com/hibiken/asynq/pull/1070)
- Add `--tls` option to dash command (PR: https://github.com/hibiken/asynq/pull/1073)
- Add `--username` CLI flag for Redis ACL authentication (PR: https://github.com/hibiken/asynq/pull/1083)
- Add `UpdateTaskPayload` method for inspector (PR: https://github.com/hibiken/asynq/pull/1042)

### Fixes
- Fix: Correct error message text in ResultWriter.Write (PR: https://github.com/hibiken/asynq/pull/1054)
- Fix: Wrap all fmt.Errorf errors with %w (PR: https://github.com/hibiken/asynq/pull/1047)
- Fix: ServeMux.NotFoundHandler returns ErrHandlerNotFound error (PR: https://github.com/hibiken/asynq/pull/1031)

### Changed
- Docs: Update server.go documentation (PR: https://github.com/hibiken/asynq/pull/1010)
- Chore: Fix godoc comment (PR: https://github.com/hibiken/asynq/pull/1009)

## [0.25.1] - 2024-12-11

### Upgrades

* Some packages

### Added

* Add `HeartbeatInterval` option to the scheduler (PR: https://github.com/hibiken/asynq/pull/956)
* Add `RedisUniversalClient` support to periodic task manager (PR: https://github.com/hibiken/asynq/pull/958)
* Add `--insecure` flag to CLI dash command (PR: https://github.com/hibiken/asynq/pull/980)
* Add logging for registration errors (PR: https://github.com/hibiken/asynq/pull/657)

### Fixes
- Perf: Use string concat inplace of fmt.Sprintf in hotpath (PR: https://github.com/hibiken/asynq/pull/962)
- Perf: Init map with size (PR: https://github.com/hibiken/asynq/pull/673)
- Fix: `Scheduler` and `PeriodicTaskManager` graceful shutdown (PR: https://github.com/hibiken/asynq/pull/977)
- Fix: `Server` graceful shutdown on UNIX systems (PR: https://github.com/hibiken/asynq/pull/982)

## [0.25.0] - 2024-10-29

### Upgrades
- Minumum go version is set to 1.22 (PR: https://github.com/hibiken/asynq/pull/925)
- Internal protobuf package is upgraded to address security advisories (PR: https://github.com/hibiken/asynq/pull/925)
- Most packages are upgraded
- CI/CD spec upgraded

### Added
- `IsPanicError` function is introduced to support catching of panic errors when processing tasks (PR: https://github.com/hibiken/asynq/pull/491)
- `JanitorInterval` and `JanitorBatchSize` are added as Server options (PR: https://github.com/hibiken/asynq/pull/715)
- `NewClientFromRedisClient` is introduced to allow reusing an existing redis client (PR: https://github.com/hibiken/asynq/pull/742)
- `TaskCheckInterval` config option is added to specify the interval between checks for new tasks to process when all queues are empty (PR: https://github.com/hibiken/asynq/pull/694)
- `Ping` method is added to Client, Server and Scheduler ((PR: https://github.com/hibiken/asynq/pull/585))
- `RevokeTask` error type is introduced to prevent a task from being retried or archived (PR: https://github.com/hibiken/asynq/pull/882)
- `SentinelUsername` is added as a redis config option (PR: https://github.com/hibiken/asynq/pull/924)
- Some jitter is introduced to improve latency when fetching jobs in the processor (PR: https://github.com/hibiken/asynq/pull/868)
- Add task enqueue command to the CLI (PR: https://github.com/hibiken/asynq/pull/918)
- Add a map cache (concurrent safe) to keep track of queues that ultimately reduces redis load when enqueuing tasks (PR: https://github.com/hibiken/asynq/pull/946)

### Fixes
- Archived tasks that are trimmed should now be deleted (PR: https://github.com/hibiken/asynq/pull/743)
- Fix lua script when listing task messages with an expired lease (PR: https://github.com/hibiken/asynq/pull/709)
- Fix potential context leaks due to cancellation not being called (PR: https://github.com/hibiken/asynq/pull/926)
- Misc documentation fixes
- Misc test fixes


## [0.24.1] - 2023-05-01

### Changed
- Updated package version dependency for go-redis 

## [0.24.0] - 2023-01-02

### Added
- `PreEnqueueFunc`, `PostEnqueueFunc` is added in `Scheduler` and deprecated `EnqueueErrorHandler` (PR: https://github.com/hibiken/asynq/pull/476)

### Changed
- Removed error log when `Scheduler` failed to enqueue a task. Use `PostEnqueueFunc` to check for errors and task actions if needed.
- Changed log level from ERROR to WARNINING when `Scheduler` failed to record `SchedulerEnqueueEvent`.

## [0.23.0] - 2022-04-11

### Added

- `Group` option is introduced to enqueue task in a group.
- `GroupAggregator` and related types are introduced for task aggregation feature.
- `GroupGracePeriod`, `GroupMaxSize`, `GroupMaxDelay`, and `GroupAggregator` fields are added to `Config`.
- `Inspector` has new methods related to "aggregating tasks".
- `Group` field is added to `TaskInfo`.
- (CLI): `group ls` command is added
- (CLI): `task ls` supports listing aggregating tasks via `--state=aggregating --group=<GROUP>` flags
- Enable rediss url parsing support

### Fixed

- Fixed overflow issue with 32-bit systems (For details, see https://github.com/hibiken/asynq/pull/426)

## [0.22.1] - 2022-02-20

### Fixed

- Fixed Redis version compatibility: Keep support for redis v4.0+

## [0.22.0] - 2022-02-19

### Added

- `BaseContext` is introduced in `Config` to specify callback hook to provide a base `context` from which `Handler` `context` is derived
- `IsOrphaned` field is added to `TaskInfo` to describe a task left in active state with no worker processing it.

### Changed

- `Server` now recovers tasks with an expired lease. Recovered tasks are retried/archived with `ErrLeaseExpired` error.

## [0.21.0] - 2022-01-22

### Added

- `PeriodicTaskManager` is added. Prefer using this over `Scheduler` as it has better support for dynamic periodic tasks.
- The `asynq stats` command now supports a `--json` option, making its output a JSON object
- Introduced new configuration for `DelayedTaskCheckInterval`. See [godoc](https://godoc.org/github.com/hibiken/asynq) for more details.

## [0.20.0] - 2021-12-19

### Added

- Package `x/metrics` is added.
- Tool `tools/metrics_exporter` binary is added.
- `ProcessedTotal` and `FailedTotal` fields were added to `QueueInfo` struct.

## [0.19.1] - 2021-12-12

### Added

- `Latency` field is added to `QueueInfo`.
- `EnqueueContext` method is added to `Client`.

### Fixed

- Fixed an error when user pass a duration less than 1s to `Unique` option

## [0.19.0] - 2021-11-06

### Changed

- `NewTask` takes `Option` as variadic argument
- Bumped minimum supported go version to 1.14 (i.e. go1.14 or higher is required).

### Added

- `Retention` option is added to allow user to specify task retention duration after completion.
- `TaskID` option is added to allow user to specify task ID.
- `ErrTaskIDConflict` sentinel error value is added.
- `ResultWriter` type is added and provided through `Task.ResultWriter` method.
- `TaskInfo` has new fields `CompletedAt`, `Result` and `Retention`.

### Removed

- `Client.SetDefaultOptions` is removed. Use `NewTask` instead to pass default options for tasks.

## [0.18.6] - 2021-10-03

### Changed

- Updated `github.com/go-redis/redis` package to v8

## [0.18.5] - 2021-09-01

### Added

- `IsFailure` config option is added to determine whether error returned from Handler counts as a failure.

## [0.18.4] - 2021-08-17

### Fixed

- Scheduler methods are now thread-safe. It's now safe to call `Register` and `Unregister` concurrently.

## [0.18.3] - 2021-08-09

### Changed

- `Client.Enqueue` no longer enqueues tasks with empty typename; Error message is returned.

## [0.18.2] - 2021-07-15

### Changed

- Changed `Queue` function to not to convert the provided queue name to lowercase. Queue names are now case-sensitive.
- `QueueInfo.MemoryUsage` is now an approximate usage value.

### Fixed

- Fixed latency issue around memory usage (see https://github.com/hibiken/asynq/issues/309).

## [0.18.1] - 2021-07-04

### Changed

- Changed to execute task recovering logic when server starts up; Previously it needed to wait for a minute for task recovering logic to exeucte.

### Fixed

- Fixed task recovering logic to execute every minute

## [0.18.0] - 2021-06-29

### Changed

- NewTask function now takes array of bytes as payload.
- Task `Type` and `Payload` should be accessed by a method call.
- `Server` API has changed. Renamed `Quiet` to `Stop`. Renamed `Stop` to `Shutdown`. _Note:_ As a result of this renaming, the behavior of `Stop` has changed. Please update the exising code to call `Shutdown` where it used to call `Stop`.
- `Scheduler` API has changed. Renamed `Stop` to `Shutdown`.
- Requires redis v4.0+ for multiple field/value pair support
- `Client.Enqueue` now returns `TaskInfo`
- `Inspector.RunTaskByKey` is replaced with `Inspector.RunTask`
- `Inspector.DeleteTaskByKey` is replaced with `Inspector.DeleteTask`
- `Inspector.ArchiveTaskByKey` is replaced with `Inspector.ArchiveTask`
- `inspeq` package is removed. All types and functions from the package is moved to `asynq` package.
- `WorkerInfo` field names have changed.
- `Inspector.CancelActiveTask` is renamed to `Inspector.CancelProcessing`

## [0.17.2] - 2021-06-06

### Fixed

- Free unique lock when task is deleted (https://github.com/hibiken/asynq/issues/275).

## [0.17.1] - 2021-04-04

### Fixed

- Fix bug in internal `RDB.memoryUsage` method.

## [0.17.0] - 2021-03-24

### Added

- `DialTimeout`, `ReadTimeout`, and `WriteTimeout` options are added to `RedisConnOpt`.

## [0.16.1] - 2021-03-20

### Fixed

- Replace `KEYS` command with `SCAN` as recommended by [redis doc](https://redis.io/commands/KEYS).

## [0.16.0] - 2021-03-10

### Added

- `Unregister` method is added to `Scheduler` to remove a registered entry.

## [0.15.0] - 2021-01-31

**IMPORTATNT**: All `Inspector` related code are moved to subpackage "github.com/hibiken/asynq/inspeq"

### Changed

- `Inspector` related code are moved to subpackage "github.com/hibken/asynq/inspeq".
- `RedisConnOpt` interface has changed slightly. If you have been passing `RedisClientOpt`, `RedisFailoverClientOpt`, or `RedisClusterClientOpt` as a pointer,
  update your code to pass as a value.
- `ErrorMsg` field in `RetryTask` and `ArchivedTask` was renamed to `LastError`.

### Added

- `MaxRetry`, `Retried`, `LastError` fields were added to all task types returned from `Inspector`.
- `MemoryUsage` field was added to `QueueStats`.
- `DeleteAllPendingTasks`, `ArchiveAllPendingTasks` were added to `Inspector`
- `DeleteTaskByKey` and `ArchiveTaskByKey` now supports deleting/archiving `PendingTask`.
- asynq CLI now supports deleting/archiving pending tasks.

## [0.14.1] - 2021-01-19

### Fixed

- `go.mod` file for CLI

## [0.14.0] - 2021-01-14

**IMPORTATNT**: Please run `asynq migrate` command to migrate from the previous versions.

### Changed

- Renamed `DeadTask` to `ArchivedTask`.
- Renamed the operation `Kill` to `Archive` in `Inpsector`.
- Print stack trace when Handler panics.
- Include a file name and a line number in the error message when recovering from a panic.

### Added

- `DefaultRetryDelayFunc` is now a public API, which can be used in the custom `RetryDelayFunc`.
- `SkipRetry` error is added to be used as a return value from `Handler`.
- `Servers` method is added to `Inspector`
- `CancelActiveTask` method is added to `Inspector`.
- `ListSchedulerEnqueueEvents` method is added to `Inspector`.
- `SchedulerEntries` method is added to `Inspector`.
- `DeleteQueue` method is added to `Inspector`.

## [0.13.1] - 2020-11-22

### Fixed

- Fixed processor to wait for specified time duration before forcefully shutdown workers.

## [0.13.0] - 2020-10-13

### Added

- `Scheduler` type is added to enable periodic tasks. See the godoc for its APIs and [wiki](https://github.com/hibiken/asynq/wiki/Periodic-Tasks) for the getting-started guide.

### Changed

- interface `Option` has changed. See the godoc for the new interface.
  This change would have no impact as long as you are using exported functions (e.g. `MaxRetry`, `Queue`, etc)
  to create `Option`s.

### Added

- `Payload.String() string` method is added
- `Payload.MarshalJSON() ([]byte, error)` method is added

## [0.12.0] - 2020-09-12

**IMPORTANT**: If you are upgrading from a previous version, please install the latest version of the CLI `go get -u github.com/hibiken/asynq/tools/asynq` and run `asynq migrate` command. No process should be writing to Redis while you run the migration command.

## The semantics of queue have changed

Previously, we called tasks that are ready to be processed _"Enqueued tasks"_, and other tasks that are scheduled to be processed in the future _"Scheduled tasks"_, etc.
We changed the semantics of _"Enqueue"_ slightly; All tasks that client pushes to Redis are _Enqueued_ to a queue. Within a queue, tasks will transition from one state to another.
Possible task states are:

- `Pending`: task is ready to be processed (previously called "Enqueued")
- `Active`: tasks is currently being processed (previously called "InProgress")
- `Scheduled`: task is scheduled to be processed in the future
- `Retry`: task failed to be processed and will be retried again in the future
- `Dead`: task has exhausted all of its retries and stored for manual inspection purpose

**These semantics change is reflected in the new `Inspector` API and CLI commands.**

---

### Changed

#### `Client`

Use `ProcessIn` or `ProcessAt` option to schedule a task instead of `EnqueueIn` or `EnqueueAt`.

| Previously                  | v0.12.0                                    |
| --------------------------- | ------------------------------------------ |
| `client.EnqueueAt(t, task)` | `client.Enqueue(task, asynq.ProcessAt(t))` |
| `client.EnqueueIn(d, task)` | `client.Enqueue(task, asynq.ProcessIn(d))` |

#### `Inspector`

All Inspector methods are scoped to a queue, and the methods take `qname (string)` as the first argument.
`EnqueuedTask` is renamed to `PendingTask` and its corresponding methods.
`InProgressTask` is renamed to `ActiveTask` and its corresponding methods.
Command "Enqueue" is replaced by the verb "Run" (e.g. `EnqueueAllScheduledTasks` --> `RunAllScheduledTasks`)

#### `CLI`

CLI commands are restructured to use subcommands. Commands are organized into a few management commands:
To view details on any command, use `asynq help <command> <subcommand>`.

- `asynq stats`
- `asynq queue [ls inspect history rm pause unpause]`
- `asynq task [ls cancel delete kill run delete-all kill-all run-all]`
- `asynq server [ls]`

### Added

#### `RedisConnOpt`

- `RedisClusterClientOpt` is added to connect to Redis Cluster.
- `Username` field is added to all `RedisConnOpt` types in order to authenticate connection when Redis ACLs are used.

#### `Client`

- `ProcessIn(d time.Duration) Option` and `ProcessAt(t time.Time) Option` are added to replace `EnqueueIn` and `EnqueueAt` functionality.

#### `Inspector`

- `Queues() ([]string, error)` method is added to get all queue names.
- `ClusterKeySlot(qname string) (int64, error)` method is added to get queue's hash slot in Redis cluster.
- `ClusterNodes(qname string) ([]ClusterNode, error)` method is added to get a list of Redis cluster nodes for the given queue.
- `Close() error` method is added to close connection with redis.

### `Handler`

- `GetQueueName(ctx context.Context) (string, bool)` helper is added to extract queue name from a context.

## [0.11.0] - 2020-07-28

### Added

- `Inspector` type was added to monitor and mutate state of queues and tasks.
- `HealthCheckFunc` and `HealthCheckInterval` fields were added to `Config` to allow user to specify a callback
  function to check for broker connection.

## [0.10.0] - 2020-07-06

### Changed

- All tasks now requires timeout or deadline. By default, timeout is set to 30 mins.
- Tasks that exceed its deadline are automatically retried.
- Encoding schema for task message has changed. Please install the latest CLI and run `migrate` command if
  you have tasks enqueued with the previous version of asynq.
- API of `(*Client).Enqueue`, `(*Client).EnqueueIn`, and `(*Client).EnqueueAt` has changed to return a `*Result`.
- API of `ErrorHandler` has changed. It now takes context as the first argument and removed `retried`, `maxRetry` from the argument list.
  Use `GetRetryCount` and/or `GetMaxRetry` to get the count values.

## [0.9.4] - 2020-06-13

### Fixed

- Fixes issue of same tasks processed by more than one worker (https://github.com/hibiken/asynq/issues/90).

## [0.9.3] - 2020-06-12

### Fixed

- Fixes the JSON number overflow issue (https://github.com/hibiken/asynq/issues/166).

## [0.9.2] - 2020-06-08

### Added

- The `pause` and `unpause` commands were added to the CLI. See README for the CLI for details.

## [0.9.1] - 2020-05-29

### Added

- `GetTaskID`, `GetRetryCount`, and `GetMaxRetry` functions were added to extract task metadata from context.

## [0.9.0] - 2020-05-16

### Changed

- `Logger` interface has changed. Please see the godoc for the new interface.

### Added

- `LogLevel` type is added. Server's log level can be specified through `LogLevel` field in `Config`.

## [0.8.3] - 2020-05-08

### Added

- `Close` method is added to `Client`.

## [0.8.2] - 2020-05-03

### Fixed

- [Fixed cancelfunc leak](https://github.com/hibiken/asynq/pull/145)

## [0.8.1] - 2020-04-27

### Added

- `ParseRedisURI` helper function is added to create a `RedisConnOpt` from a URI string.
- `SetDefaultOptions` method is added to `Client`.

## [0.8.0] - 2020-04-19

### Changed

- `Background` type is renamed to `Server`.
- To upgrade from the previous version, Update `NewBackground` to `NewServer` and pass `Config` by value.
- CLI is renamed to `asynq`.
- To upgrade the CLI to the latest version run `go get -u github.com/hibiken/tools/asynq`
- The `ps` command in CLI is renamed to `servers`
- `Concurrency` defaults to the number of CPUs when unset or set to a negative value.

### Added

- `ShutdownTimeout` field is added to `Config` to speicfy timeout duration used during graceful shutdown.
- New `Server` type exposes `Start`, `Stop`, and `Quiet` as well as `Run`.

## [0.7.1] - 2020-04-05

### Fixed

- Fixed signal handling for windows.

## [0.7.0] - 2020-03-22

### Changed

- Support Go v1.13+, dropped support for go v1.12

### Added

- `Unique` option was added to allow client to enqueue a task only if it's unique within a certain time period.

## [0.6.2] - 2020-03-15

### Added

- `Use` method was added to `ServeMux` to apply middlewares to all handlers.

## [0.6.1] - 2020-03-12

### Added

- `Client` can optionally schedule task with `asynq.Deadline(time)` to specify deadline for task's context. Default is no deadline.
- `Logger` option was added to config, which allows user to specify the logger used by the background instance.

## [0.6.0] - 2020-03-01

### Added

- Added `ServeMux` type to make it easy for users to implement Handler interface.
- `ErrorHandler` type was added. Allow users to specify error handling function (e.g. Report error to error reporting service such as Honeybadger, Bugsnag, etc)

## [0.5.0] - 2020-02-23

### Changed

- `Client` API has changed. Use `Enqueue`, `EnqueueAt` and `EnqueueIn` to enqueue and schedule tasks.

### Added

- `asynqmon workers` was added to list all running workers information

## [0.4.0] - 2020-02-13

### Changed

- `Handler` interface has changed. `ProcessTask` method takes two arguments `context.Context` and `*asynq.Task`
- `Queues` field in `Config` has change from `map[string]uint` to `map[string]int`

### Added

- `Client` can optionally schedule task with `asynq.Timeout(duration)` to specify timeout duration for task. Default is no timeout.
- `asynqmon cancel [task id]` will send a cancelation signal to the goroutine processing the speicified task.

## [0.3.0] - 2020-02-04

### Added

- `asynqmon ps` was added to list all background worker processes

## [0.2.2] - 2020-01-26

### Fixed

- Fixed restoring unfinished tasks back to correct queues.

### Changed

- `asynqmon ls` command is now paginated (default 30 tasks from first page)
- `asynqmon ls enqueued:[queue name]` requires queue name to be specified

## [0.2.1] - 2020-01-22

### Fixed

- More structured log messages
- Prevent spamming logs with a bunch of errors when Redis connection is lost
- Fixed and updated README doc

## [0.2.0] - 2020-01-19

### Added

- NewTask constructor
- `Queues` option in `Config` to specify mutiple queues with priority level
- `Client` can schedule a task with `asynq.Queue(name)` to specify which queue to use
- `StrictPriority` option in `Config` to specify whether the priority should be followed strictly
- `RedisConnOpt` to abstract away redis client implementation
- [CLI] `asynqmon rmq` command to remove queue

### Changed

- `Client` and `Background` constructors take `RedisConnOpt` as their first argument.
- `asynqmon stats` now shows the total of all enqueued tasks under "Enqueued"
- `asynqmon stats` now shows each queue's task count
- `asynqmon history` now doesn't take any arguments and shows data from the last 10 days by default (use `--days` flag to change the number of days)
- Task type is now immutable (i.e., Payload is read-only)

## [0.1.0] - 2020-01-04

### Added

- Initial version of asynq package
- Initial version of asynqmon CLI
