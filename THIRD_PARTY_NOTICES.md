# Third-party notices and corresponding source

This file supplements the third-party notices in `LICENSE`. The MIT license
for WeKnora's own code does not replace the licenses of third-party components.
Keep this file, `LICENSE`, and the `licenses/` directory with redistributed
backend and desktop packages. Container distributions include them in `/app`;
macOS applications include them in `Contents/Resources`.
Windows installers place them next to the installed executable.

## Go MySQL Driver

- Component: `github.com/go-sql-driver/mysql`, version `v1.10.0`.
- Used in backend and desktop binaries, including the Doris MySQL protocol driver.
- License: Mozilla Public License 2.0, reproduced in
  [`licenses/go-sql-driver-mysql-MPL-2.0.txt`](licenses/go-sql-driver-mysql-MPL-2.0.txt).
- Copyright: The Go-MySQL-Driver Authors; the original per-file notices and
  `AUTHORS` are preserved in the accompanying source archive.
- Modifications by WeKnora: none. Dialer configuration is in separate WeKnora files.
- Corresponding source, available under MPL-2.0:
  [`licenses/sources/mysql-v1.10.0.zip`](licenses/sources/mysql-v1.10.0.zip).
  The same version is available from the
  [Go module proxy](https://proxy.golang.org/github.com/go-sql-driver/mysql/@v/v1.10.0.zip)
  and [upstream repository](https://github.com/go-sql-driver/mysql/tree/v1.10.0).

## go-m1cpu

- Component: `github.com/shoenig/go-m1cpu`, version `v0.1.6`.
- Used by the macOS backend/desktop dependency chain through gopsutil. It is
  absent from Linux backend build dependencies. Its source is included in all
  notice bundles for consistent packaging; this does not imply Linux linkage.
- License: Mozilla Public License 2.0, reproduced in
  [`licenses/go-m1cpu-MPL-2.0.txt`](licenses/go-m1cpu-MPL-2.0.txt).
- Attribution: the go-m1cpu project and its contributors. The complete original
  source and notices are preserved in the accompanying archive.
- Modifications by WeKnora: none.
- Corresponding source, available under MPL-2.0:
  [`licenses/sources/go-m1cpu-v0.1.6.zip`](licenses/sources/go-m1cpu-v0.1.6.zip).
  The same version is available from the
  [Go module proxy](https://proxy.golang.org/github.com/shoenig/go-m1cpu/@v/v0.1.6.zip)
  and [upstream repository](https://github.com/shoenig/go-m1cpu/tree/v0.1.6).

## OpenCC dictionary data

- Files: `internal/textconv/data/TSPhrases.txt` and `TSCharacters.txt`, copied
  unchanged from `github.com/longbridgeapp/opencc` version `v0.3.13`.
- Attribution: the OpenCC and longbridge/opencc contributors.
- License: Apache-2.0, reproduced in
  [`licenses/OpenCC-Apache-2.0.txt`](licenses/OpenCC-Apache-2.0.txt).
- Source: [versioned dictionary directory](https://github.com/longbridgeapp/opencc/tree/v0.3.13/dictionary)
  and the two text files distributed with WeKnora's source.
- Only dictionary data is retained. WeKnora uses its own standard-library lookup
  implementation; the upstream Go converter, `liuzl/da`, and GPL-licensed
  `cedar-go` code are not included.

## Build-only component

The Windows installer template in `cmd/desktop/build/windows/installer/project.nsi`
is based on Wails v2.12.0's MIT-licensed default template, with local changes to
install this notice/source bundle. Its license and copyright are reproduced in
[`licenses/Wails-MIT.txt`](licenses/Wails-MIT.txt).

`cbindgen` 0.29.4 (MPL-2.0) generates the AnyDoc C ABI header during the Rust
build. Its code/tool executable is not copied into the runtime image or release
packages by the current build recipes. Generating the header does not copy the
tool's implementation into the product. If distributing build environments or
the tool itself, retain its license and provide its corresponding source too.

## Maintaining this bundle

When upgrading either MPL module, update the version, license, and matching
source archive together. Archives are unmodified Go module proxy ZIPs, including
their original copyright notices. Verify them with `go mod download -json` and
the module's `go.sum` entry before updating. `scripts/check-license-bundle.sh`
checks the pins and packaging inputs without downloading dependencies.
