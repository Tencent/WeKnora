#!/usr/bin/env bash
set -euo pipefail

sync_mode="${1:---check}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "${script_dir}/.." && pwd)"
workspace_dir="$(cd "${repo_dir}/.." && pwd)"
source_dir="${workspace_dir}/skill/extract-video-knowledge-v2"
target_dir="${repo_dir}/skills/preloaded/extract-video-knowledge"
target_parent="$(dirname "${target_dir}")"

case "${sync_mode}" in
  --check|--write) ;;
  *)
    echo "usage: $0 [--check|--write]" >&2
    exit 2
    ;;
esac

if [[ ! -f "${source_dir}/SKILL.md" || ! -f "${source_dir}/agents/openai.yaml" ]]; then
  echo "V2 source skill is incomplete: ${source_dir}" >&2
  exit 1
fi

mkdir -p "${target_parent}"
staging_dir="$(mktemp -d "${target_parent}/.extract-video-knowledge-v2.XXXXXX")"
backup_dir=""
cleanup() {
  [[ -z "${staging_dir}" || ! -e "${staging_dir}" ]] || rm -rf "${staging_dir}"
  if [[ -n "${backup_dir}" && -e "${backup_dir}" ]]; then
    if [[ ! -e "${target_dir}" ]]; then
      mv "${backup_dir}" "${target_dir}"
    else
      rm -rf "${backup_dir}"
    fi
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
cp -R "${source_dir}/." "${staging_dir}/"

# Runtime metadata keeps the deployed skill name while all behavioral content
# remains generated from the V2 source.
perl -0pi -e 's/^name: extract-video-knowledge-v2$/name: extract-video-knowledge/m' "${staging_dir}/SKILL.md"
perl -0pi -e 's/\$extract-video-knowledge-v2/\$extract-video-knowledge/g' "${staging_dir}/agents/openai.yaml"

if [[ "${sync_mode}" == "--write" ]]; then
  backup_dir="$(mktemp -d "${target_parent}/.extract-video-knowledge-backup.XXXXXX")"
  rmdir "${backup_dir}"
  if [[ -e "${target_dir}" ]]; then
    mv "${target_dir}" "${backup_dir}"
  else
    backup_dir=""
  fi
  if ! mv "${staging_dir}" "${target_dir}"; then
    [[ -z "${backup_dir}" || ! -e "${backup_dir}" ]] || mv "${backup_dir}" "${target_dir}"
    exit 1
  fi
  staging_dir=""
  [[ -z "${backup_dir}" || ! -e "${backup_dir}" ]] || rm -rf "${backup_dir}"
  backup_dir=""
else
  if ! diff -ru "${staging_dir}" "${target_dir}"; then
    echo "runtime skill is not synchronized from V2 source" >&2
    exit 1
  fi
fi

echo "extract-video-knowledge V2 runtime copy is synchronized"
