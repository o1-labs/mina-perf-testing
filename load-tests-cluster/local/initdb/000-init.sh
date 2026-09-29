#!/usr/bin/env bash
# Runs once, on an empty data directory (postgres' docker-entrypoint-initdb.d).
#
# 1. Apply the cluster's shared schema from ../init-sql, in file order.
#    It is mounted at /init-sql rather than into initdb.d, so this local file
#    does not have to be placed inside the shared directory.
# 2. Record the local deployment. The orchestrator service reads the release
#    of the latest deployment to decide which mina client may fund keys; a
#    release that names the commit of its bundled client lets it proceed.
set -euo pipefail

psql_run() { psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" "$@"; }

for f in /init-sql/*.sql; do
  echo "applying $f"
  psql_run -f "$f"
done

psql_run -v release="$MINA_RELEASE" <<'SQL'
INSERT INTO deployment (metadata_json)
VALUES (jsonb_build_object('release', :'release', 'comment', 'local network'));
SQL
