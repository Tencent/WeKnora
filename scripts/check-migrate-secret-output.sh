#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT
cat > "$TEMP_DIR/weknora-migrate" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "$MIGRATE_CALLED_FILE"
case "$1" in
  inspect|plan|version|apply|up) exit 0 ;;
  *) echo 'unsupported migration command' >&2; exit 2 ;;
esac
EOF
chmod +x "$TEMP_DIR/weknora-migrate"
sentinel='MIGRATION_CREDENTIAL_MUST_NOT_REACH_OUTPUT_7f31a9'
for action in inspect plan version apply up down force goto; do
    status=0
    output=$(env WEKNORA_MIGRATE_BIN="$TEMP_DIR/weknora-migrate" \
        MIGRATE_CALLED_FILE="$TEMP_DIR/called-$action" \
        DB_PASSWORD="$sentinel" \
        DB_URL="postgres://fixture:${sentinel}@db.invalid/test?sslmode=verify-full" \
        bash "$ROOT_DIR/scripts/migrate.sh" "$action" 2>&1) || status=$?
    expected=0
    case "$action" in down|force|goto) expected=2 ;; esac
    test "$status" -eq "$expected"
    test -f "$TEMP_DIR/called-$action"
    test "$(head -n 1 "$TEMP_DIR/called-$action")" = "$action"
    if grep -Fq "$sentinel" <<< "$output"; then
        echo 'migration command exposed a credential' >&2
        exit 1
    fi
done
echo 'migration wrapper routing, rejection status and credential-output checks passed'
