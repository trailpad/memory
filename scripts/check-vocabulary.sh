#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
failed=0
while IFS= read -r -d '' file; do
  case "$file" in
    LICENSE|internal/store/postgres/migrations/0001_records.up.sql|internal/store/postgres/migrations/0001_records.down.sql|internal/store/postgres/migrations/0005_episodic.up.sql|internal/store/postgres/migrations/0005_episodic.down.sql|internal/store/postgres/migrations/0006_record_type.up.sql|internal/store/postgres/migrations/0006_record_type.down.sql|internal/store/postgres/migrate_test.go|internal/api/http/episodic_test.go|AGENTS.md|scripts/check-vocabulary.sh) continue ;;
  esac
  [[ -f "$file" ]] || continue
  if LC_ALL=C grep -niwE 'kind' -- "$file"; then
    printf 'Forbidden vocabulary in %s\n' "$file" >&2
    failed=1
  fi
done < <(git ls-files --cached --others --exclude-standard -z)
exit "$failed"
