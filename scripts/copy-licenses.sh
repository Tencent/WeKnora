#!/usr/bin/env bash
set -euo pipefail

# Include this directory unchanged alongside every backend/desktop distribution.
if [ "$#" -ne 1 ] || [ -z "$1" ]; then
    echo "Usage: $0 DESTINATION_DIRECTORY" >&2
    exit 1
fi

license_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
license_dest="$1"
bash "${license_root}/scripts/check-license-bundle.sh"
mkdir -p "${license_dest}/licenses"
cp "${license_root}/LICENSE" "${license_root}/THIRD_PARTY_NOTICES.md" "${license_dest}/"
cp -R "${license_root}/licenses/." "${license_dest}/licenses/"
