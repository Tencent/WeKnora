# Third-party notices and corresponding source

This file supplements the third-party notices in `LICENSE`. The MIT license
for WeKnora's own code does not replace the licenses of third-party components.
Keep this file, `LICENSE`, and the `licenses/` directory with redistributed
backend and desktop packages. `scripts/copy-licenses.sh` adds checksum-verified
source archives at packaging time; they are not stored in this Git repository.
Container distributions include the complete bundle in `/app`;
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
- Corresponding source, available under MPL-2.0, is included in binary releases
  as `licenses/sources/mysql-v1.10.0.zip`. Source repository users can obtain it from the
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
- Corresponding source, available under MPL-2.0, is included in binary releases
  as `licenses/sources/go-m1cpu-v0.1.6.zip`. Source repository users can obtain it from the
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

## Goldmark

- Component: `github.com/yuin/goldmark`, version `v1.8.2`.
- Used to identify GFM tables for Wiki machine-write row preservation.
- License: MIT, reproduced in [`licenses/goldmark-MIT.txt`](licenses/goldmark-MIT.txt).
- Copyright: 2019 Yusuke Inuzuka. Modifications by WeKnora: none.
- Source: [upstream repository](https://github.com/yuin/goldmark/tree/v1.8.2).

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

When upgrading either MPL module, update the version, license, and SHA-256 pin in
`licenses/sources/modules.tsv` together. Archives are unmodified Go module proxy
ZIPs, including their original copyright notices. Verify them with
`go mod download -json` and the module's `go.sum` entry before updating.
`scripts/check-license-bundle.sh` checks the pins and notices without downloading
dependencies. `scripts/copy-licenses.sh DESTINATION` obtains the pinned archives
through Go's configured `GOPROXY` and module cache, verifies their SHA-256 hashes,
and includes them in the destination bundle. Offline packaging requires those
module archives to be present in the Go module cache beforehand. To verify a
packaged source directory, run `scripts/check-license-bundle.sh PATH/TO/licenses/sources`.

## Docreader OCR page-preprocessing components (Python)

The docreader service straightens and crops photographed pages before OCR. That
capability adds the following Python components to the docreader image. They are
redistributed inside the container image, so their notices travel here rather
than in `licenses/sources/modules.tsv`, which records Go modules only
(`scripts/check-license-bundle.sh` verifies that manifest against `go.mod`).

### RapidOCR

- Component: `rapidocr`, version `3.10.0` (Python package, docreader service).
- Used in the docreader service to detect text lines and score their
  recognition, which is what the page-cropping pipeline keys off.
- License: Apache License 2.0, reproduced in
  [`licenses/rapidocr-Apache-2.0.txt`](licenses/rapidocr-Apache-2.0.txt).
- Copyright: The RapidOCR Authors.
- Model artifacts: RapidOCR redistributes OCR model weights whose copyright is
  held separately by Baidu and/or the applicable PaddleOCR rights holders. They
  are distributed under Apache-2.0 as well, and their provenance and
  redistribution terms are recorded in
  [`licenses/rapidocr-MODEL_LICENSES.md`](licenses/rapidocr-MODEL_LICENSES.md).
- Modifications by WeKnora: none. The integration calls the published API and
  adds no changes to the component.
- Corresponding source: [PyPI](https://pypi.org/project/rapidocr/3.10.0/) and
  [upstream repository](https://github.com/RapidAI/RapidOCR).

### ONNX Runtime

- Component: `onnxruntime`, version `1.23.2` (Python package, docreader service).
- Used as the inference engine RapidOCR runs its models on.
- License: MIT License, reproduced in
  [`licenses/onnxruntime-MIT.txt`](licenses/onnxruntime-MIT.txt).
- Copyright: Microsoft Corporation.
- Modifications by WeKnora: none.
- Corresponding source: [PyPI](https://pypi.org/project/onnxruntime/1.23.2/) and
  [upstream repository](https://github.com/microsoft/onnxruntime).

### OpenCV (headless)

- Component: `opencv-python-headless`, version `5.0.0.93` (Python package,
  docreader service).
- Used for geometric image operations: rotation with canvas expansion, line
  intersection, homography and warping.
- License: Apache License 2.0, reproduced in
  [`licenses/opencv-python-headless-Apache-2.0.txt`](licenses/opencv-python-headless-Apache-2.0.txt);
  bundled third-party components are listed in
  [`licenses/opencv-python-headless-THIRD-PARTY.txt`](licenses/opencv-python-headless-THIRD-PARTY.txt).
- Copyright: The OpenCV Foundation and contributors (see the notice file for
  the per-component attributions it carries).
- Modifications by WeKnora: none.
- Corresponding source: [PyPI](https://pypi.org/project/opencv-python-headless/5.0.0.93/)
  and [upstream repository](https://github.com/opencv/opencv-python).

### NumPy

- Component: `numpy`, version `2.3.4` (Python package, docreader service).
- Used for array manipulation throughout the page-preprocessing pipeline.
- License: BSD 3-Clause License, reproduced in
  [`licenses/numpy-BSD-3-Clause.txt`](licenses/numpy-BSD-3-Clause.txt).
- Copyright: The NumPy Developers.
- Modifications by WeKnora: none.
- Corresponding source: [PyPI](https://pypi.org/project/numpy/2.3.4/) and
  [upstream repository](https://github.com/numpy/numpy).
