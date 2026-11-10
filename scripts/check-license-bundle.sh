#!/usr/bin/env bash
set -euo pipefail

license_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_dir="${1:-}"
if [ -n "${source_dir}" ]; then
    source_dir="$(cd "${source_dir}" && pwd)"
fi
cd "${license_root}"

# A retained build-graph dependency would bring the GPL converter back even if
# application code no longer imported it directly.
if grep -Eq 'github.com/(longbridgeapp/opencc|liuzl/(da|cedar-go)|adamzy/cedar-go)([[:space:]]|$)' go.mod go.sum; then
    echo "Removed conversion dependencies have reappeared in go.mod/go.sum" >&2
    exit 1
fi

test -s licenses/sources/modules.tsv
while read -r module_name module_version checksum; do
    [[ "${checksum}" =~ ^[0-9a-f]{64}$ ]] || { echo "Invalid source checksum for ${module_name}" >&2; exit 1; }
    actual_version="$(awk -v name="${module_name}" '$1 == name { print $2 }' go.mod)"
    if [ "${actual_version}" != "${module_version}" ]; then
        echo "Update the source manifest for ${module_name}: expected ${module_version}, got ${actual_version}" >&2
        exit 1
    fi
    if [ -n "${source_dir}" ]; then
        archive="${module_name##*/}-${module_version}.zip"
        (
            cd "${source_dir}"
            if command -v sha256sum >/dev/null 2>&1; then
                printf '%s  %s\n' "${checksum}" "${archive}" | sha256sum -c -
            else
                printf '%s  %s\n' "${checksum}" "${archive}" | shasum -a 256 -c -
            fi
        )
    fi
done < licenses/sources/modules.tsv

for file in LICENSE THIRD_PARTY_NOTICES.md licenses/OpenCC-Apache-2.0.txt \
    licenses/go-sql-driver-mysql-MPL-2.0.txt licenses/go-m1cpu-MPL-2.0.txt \
    licenses/Wails-MIT.txt; do
    test -s "${file}"
done
