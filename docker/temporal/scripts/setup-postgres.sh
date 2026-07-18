#!/bin/sh
set -eu

run_tool() {
  db="$1"
  shift

  temporal-sql-tool \
    --plugin postgres12 \
    --ep "$SQL_HOST" \
    -p "$SQL_PORT" \
    -u "$SQL_USER" \
    -pw "$SQL_PASSWORD" \
    --db "$db" \
    "$@"
}

run_tool temporal setup-schema -v 0.0 || true
run_tool temporal update-schema -d /etc/temporal/schema/postgresql/v12/temporal/versioned

run_tool temporal_visibility setup-schema -v 0.0 || true
run_tool temporal_visibility update-schema -d /etc/temporal/schema/postgresql/v12/visibility/versioned
