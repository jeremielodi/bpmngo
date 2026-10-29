#!/bin/sh
set -eu

if [ "$#" -lt 6 ] || [ "$#" -gt 9 ]; then
  echo "usage: $0 ENGINE ADDRESS REST USER PASSWORD 'containers' [duration] [clients] [postgres-container]" >&2
  exit 2
fi

engine=$1
address=$2
rest=$3
user=$4
password=$5
containers=$6
duration=${7:-30m}
clients=${8:-100}
postgres=${9:-bpmngo-benchmark-postgres}
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
out="$root/benchmark/raw/$engine/endurance-${duration}-${clients}c"

mkdir -p "$out"
docker exec "$postgres" psql -X -U goflow -d goflow -v ON_ERROR_STOP=1 \
  -c "SELECT pg_stat_statements_reset()" \
  -c "SELECT pg_stat_reset_shared('wal')" \
  -c "SELECT pg_stat_reset_shared('bgwriter')" >/dev/null

"$root/benchmark/scripts/capture-postgres-stats.sh" "$out/before" "$postgres"
"$root/benchmark/scripts/measure-command.sh" "$out/load" "$containers" -- \
  "$root/benchmark/bin/bench" -mode load -engine "$engine" -address "$address" -rest "$rest" \
  -user "$user" -password "$password" -clients "$clients" -workers 8 -batch 32 \
  -warmup 60s -duration "$duration" -output "$out/load/result.json"
"$root/benchmark/scripts/capture-postgres-stats.sh" "$out/after" "$postgres"

echo "endurance results: $out"
