#!/bin/sh
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 4 ]; then
  echo "usage: $0 OUTPUT_DIR POSTGRES_CONTAINER [PGBOUNCER_HOST] [PASSWORD]" >&2
  exit 2
fi

out=$1
postgres_container=$2
host=${3:-bpmngo-pgbouncer}
password=${4:-goflow123}
mkdir -p "$out"

capture() {
  command=$1
  file=$2
  docker exec -e PGPASSWORD="$password" "$postgres_container" \
    psql -X -h "$host" -p 5432 -U goflow -d pgbouncer --csv -c "$command" > "$out/$file.csv"
}

date -u +%Y-%m-%dT%H:%M:%SZ > "$out/captured-at.txt"
capture "SHOW POOLS" pools
capture "SHOW STATS" stats
capture "SHOW SERVERS" servers
