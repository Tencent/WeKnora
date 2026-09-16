#!/usr/bin/env bash
set -euo pipefail

license_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${license_root}"

# A retained build-graph dependency would bring the GPL converter back even if
# application code no longer imported it directly.
if grep -Eq 'github.com/(longbridgeapp/opencc|liuzl/(da|cedar-go)|adamzy/cedar-go)([[:space:]]|$)' go.mod go.sum; then
    echo "Removed conversion dependencies have reappeared in go.mod/go.sum" >&2
    exit 1
fi

for module_pin in 'github.com/go-sql-driver/mysql v1.10.0' 'github.com/shoenig/go-m1cpu v0.1.6'; do
    read -r module_name module_version <<< "${module_pin}"
    actual_version="$(awk -v name="${module_name}" '$1 == name { print $2 }' go.mod)"
    if [ "${actual_version}" != "${module_version}" ]; then
        echo "Update the license/source bundle for ${module_name}: expected ${module_version}, got ${actual_version}" >&2
        exit 1
    fi
done

for file in LICENSE THIRD_PARTY_NOTICES.md licenses/OpenCC-Apache-2.0.txt \
    licenses/go-sql-driver-mysql-MPL-2.0.txt licenses/go-m1cpu-MPL-2.0.txt \
    licenses/Wails-MIT.txt; do
    test -s "${file}"
done

cd licenses/sources
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
else
    shasum -a 256 -c SHA256SUMS
fi
