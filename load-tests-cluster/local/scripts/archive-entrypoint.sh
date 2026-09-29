#!/usr/bin/env bash
# Starts the archive process of the local network (ARCHIVE=1).
#
# The archive database is new for every chain: `make up` removes it together
# with the nodes. On an empty database this applies the schema that ships in
# the archive image, then runs the archive with the same runtime config as
# the daemons, so the archive records the same genesis ledger.
set -euo pipefail

: "${ARCHIVE_DB_URI:?}"

until pg_isready -q -d "$ARCHIVE_DB_URI"; do sleep 1; done

if ! psql -tA -d "$ARCHIVE_DB_URI" -c "SELECT to_regclass('public.blocks')" | grep -q blocks; then
  echo "applying /etc/mina/archive/create_schema.sql"
  psql -v ON_ERROR_STOP=1 -q -d "$ARCHIVE_DB_URI" -f /etc/mina/archive/create_schema.sql
fi

exec mina-archive run \
  --postgres-uri "$ARCHIVE_DB_URI" \
  --server-port 3086 \
  --config-file /local/runtime-config.json \
  --log-level Info
