#!/usr/bin/env bash
# Build the anydoc static archive that the `anydoc` build tag links.
#
# The archive is a Rust build artifact (~30 MB), so it is neither committed nor
# downloaded: this script builds it from third_party/anydoc-go, which pins the
# published anydoc crate, and drops it where cgo looks for it.
#
# Usage:
#   scripts/build-anydoc-lib.sh              # host platform
#   TARGET=aarch64-unknown-linux-musl scripts/build-anydoc-lib.sh
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
crate_dir="$repo_root/third_party/anydoc-go"

if ! command -v cargo >/dev/null 2>&1; then
  echo "error: cargo not found. Install Rust (https://rustup.rs) to build the anydoc archive." >&2
  exit 1
fi

# The Rust target triple decides the archive; the directory name mirrors what
# the cgo LDFLAGS in third_party/anydoc-go expect for that platform.
target=${TARGET:-$(rustc -vV | sed -n 's/^host: //p')}
case "$target" in
  x86_64-apple-darwin) lib_dir=darwin_amd64 ;;
  aarch64-apple-darwin) lib_dir=darwin_arm64 ;;
  x86_64-pc-windows-msvc) lib_dir=windows_amd64 ;;
  x86_64-unknown-linux-gnu) lib_dir=linux_amd64_gnu ;;
  aarch64-unknown-linux-gnu) lib_dir=linux_arm64_gnu ;;
  x86_64-unknown-linux-musl) lib_dir=linux_amd64_musl ;;
  aarch64-unknown-linux-musl) lib_dir=linux_arm64_musl ;;
  *)
    echo "error: unsupported target '$target'." >&2
    echo "Supported: {x86_64,aarch64}-{apple-darwin,unknown-linux-{gnu,musl}}, x86_64-pc-windows-msvc" >&2
    exit 1
    ;;
esac

case "$target" in
  *windows-msvc) lib_name=anydoc_go.lib ;;
  *) lib_name=libanydoc_go.a ;;
esac

# The crate version to patch is read from Cargo.toml rather than repeated here,
# so an upgrade is a one-line change to the `anydoc = "=X.Y.Z"` pin.
anydoc_version=$(sed -n 's/^anydoc = "=\([0-9][^"]*\)".*/\1/p' "$crate_dir/Cargo.toml" | head -1)
if [ -z "$anydoc_version" ]; then
  echo "error: no pinned 'anydoc = \"=X.Y.Z\"' dependency in $crate_dir/Cargo.toml" >&2
  exit 1
fi

# document_to_markdown is private in published anydoc. Copy the pinned version
# and re-export that one function so anydoc-go can keep the official serializer.
prepare_patched_anydoc() {
  local dest="$crate_dir/patched-anydoc"
  # The marker records which version was patched: after a version bump the
  # copy left by the previous build is the wrong crate, and reusing it would
  # fail deep inside cargo's patch resolution instead of here. Cargo.toml is
  # required too: a truncated leftover with only the marker would otherwise
  # skip the copy and fail with "failed to read .../Cargo.toml".
  if [ -f "$dest/.weknora-patched" ] && [ "$(cat "$dest/.weknora-patched")" = "$anydoc_version" ] && [ -f "$dest/Cargo.toml" ]; then
    return
  fi

  local src=""
  local cargo_home="${CARGO_HOME:-$HOME/.cargo}"
  src=$(find "$cargo_home/registry/src" -maxdepth 2 -type d -name "anydoc-$anydoc_version" 2>/dev/null | head -1 || true)

  if [ -z "$src" ]; then
    local tarball="$crate_dir/.anydoc-$anydoc_version.crate"
    echo "Fetching anydoc $anydoc_version to patch document_to_markdown into the public API"
    if ! curl -fsSL "https://rsproxy.cn/api/v1/crates/anydoc/$anydoc_version/download" -o "$tarball"; then
      curl -fsSL "https://static.crates.io/crates/anydoc/anydoc-$anydoc_version.crate" -o "$tarball"
    fi
    local unpack="$crate_dir/.anydoc-unpack"
    rm -rf "$unpack"
    mkdir -p "$unpack"
    tar -xzf "$tarball" -C "$unpack"
    src=$(find "$unpack" -maxdepth 1 -type d -name "anydoc-$anydoc_version" | head -1)
    if [ -z "$src" ]; then
      echo "error: unpacked anydoc-$anydoc_version crate is missing" >&2
      exit 1
    fi
  fi

  rm -rf "$dest"
  cp -R "$src" "$dest"
  if ! grep -q '^use render::markdown::document_to_markdown;$' "$dest/src/lib.rs"; then
    echo "error: anydoc $anydoc_version lib.rs no longer has the expected document_to_markdown import" >&2
    exit 1
  fi
  sed -i.bak 's/^use render::markdown::document_to_markdown;/pub use render::markdown::document_to_markdown;/' "$dest/src/lib.rs"
  rm -f "$dest/src/lib.rs.bak"
  printf '%s\n' "$anydoc_version" > "$dest/.weknora-patched"
  rm -rf "$crate_dir/.anydoc-unpack" "$crate_dir/.anydoc-$anydoc_version.crate"
}

# version.go is what the parser reports as `anydoc_version` on every parsed
# document, so a bump that misses it would mislabel the whole knowledge base.
go_version=$(sed -n 's/^const Version = "\([^"]*\)".*/\1/p' "$crate_dir/version.go" | head -1)
if [ "$go_version" != "$anydoc_version" ]; then
  echo "error: version.go says '$go_version' but Cargo.toml pins anydoc '$anydoc_version'." >&2
  echo "Bump both: the Go constant is the version WeKnora records for parsed documents." >&2
  exit 1
fi

echo "Building anydoc $anydoc_version archive for $target"
prepare_patched_anydoc

# --locked: build exactly the dependency versions in the committed Cargo.lock.
# That lockfile is what pins the patched pdf-inspector/lopdf, so a silent
# resolver drift must fail the build rather than ship an unaudited tree.
cargo build --release --locked --manifest-path "$crate_dir/Cargo.toml" --target "$target"

dest="$crate_dir/lib/$lib_dir"
mkdir -p "$dest"
cp "$crate_dir/target/$target/release/$lib_name" "$dest/$lib_name"

echo "Wrote $dest/$lib_name"
echo "Build WeKnora with the archive: go build -tags anydoc ./cmd/server"
