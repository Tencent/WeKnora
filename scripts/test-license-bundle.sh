#!/usr/bin/env bash
# Exercise packaging without network access or a populated developer module cache.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
mkdir -p "${fixture}/scripts" "${fixture}/bin" "${fixture}/licenses/sources"
cp "${repo_root}/scripts/"{check-license-bundle,copy-licenses}.sh "${fixture}/scripts/"
cp "${repo_root}/LICENSE" "${repo_root}/THIRD_PARTY_NOTICES.md" "${fixture}/"
cp "${repo_root}/licenses/"*.txt "${fixture}/licenses/"
touch "${fixture}/go.sum"

checksum() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# Supply two deterministic stand-ins for downloaded archives. This test verifies
# the packager's integrity gate, not Go's module download implementation.
export LICENSE_TEST_CACHE="${fixture}/module cache"
for pin in 'github.com/go-sql-driver/mysql v1.10.0' 'github.com/shoenig/go-m1cpu v0.1.6'; do
    read -r module version <<< "${pin}"
    cache_dir="${LICENSE_TEST_CACHE}/cache/download/${module}/@v"
    mkdir -p "${cache_dir}"
    printf 'source for %s\n' "${pin}" > "${cache_dir}/${version}.zip"
    printf '%s %s %s\n' "${module}" "${version}" "$(checksum "${cache_dir}/${version}.zip")" \
        >> "${fixture}/licenses/sources/modules.tsv"
    printf '%s\n' "${pin}" >> "${fixture}/go.mod"
done
cat > "${fixture}/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
    'env GOMODCACHE') printf '%s\n' "${LICENSE_TEST_CACHE}" ;;
    'mod download '*) exit "${LICENSE_TEST_DOWNLOAD_FAILURE:-0}" ;;
    *) echo "Unexpected go invocation: $*" >&2; exit 1 ;;
esac
EOF
chmod +x "${fixture}/bin/go"
export PATH="${fixture}/bin:${PATH}"

bash "${fixture}/scripts/check-license-bundle.sh"
bash "${fixture}/scripts/copy-licenses.sh" "${fixture}/release with spaces"
for pin in 'mysql v1.10.0' 'go-m1cpu v0.1.6'; do
    read -r name version <<< "${pin}"
    test -s "${fixture}/release with spaces/licenses/sources/${name}-${version}.zip"
done
cmp "${fixture}/LICENSE" "${fixture}/release with spaces/LICENSE"
cmp "${fixture}/THIRD_PARTY_NOTICES.md" "${fixture}/release with spaces/THIRD_PARTY_NOTICES.md"
bash "${fixture}/scripts/check-license-bundle.sh" "${fixture}/release with spaces/licenses/sources"

if LICENSE_TEST_DOWNLOAD_FAILURE=1 bash "${fixture}/scripts/copy-licenses.sh" "${fixture}/failed download"; then
    echo 'Packaging accepted a failed source download' >&2; exit 1
fi
test ! -e "${fixture}/failed download"

printf 'tampered source\n' > "${LICENSE_TEST_CACHE}/cache/download/github.com/go-sql-driver/mysql/@v/v1.10.0.zip"
if bash "${fixture}/scripts/copy-licenses.sh" "${fixture}/corrupt release" > "${fixture}/corrupt.log" 2>&1; then
    echo 'Packaging accepted a mismatched source checksum' >&2; exit 1
fi
test ! -e "${fixture}/corrupt release"

printf 'github.com/go-sql-driver/mysql v0.0.0\n' > "${fixture}/go.mod"
if bash "${fixture}/scripts/check-license-bundle.sh" > "${fixture}/version.log" 2>&1; then
    echo 'Notice validation accepted a stale module version' >&2; exit 1
fi
echo 'License bundle packaging tests passed'
