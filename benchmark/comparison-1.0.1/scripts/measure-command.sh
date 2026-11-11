#!/bin/sh
set -eu

if [ "$#" -lt 4 ]; then
  echo "usage: $0 OUT_DIR 'container1 container2' -- command..." >&2
  exit 2
fi

out=$1
containers=$2
shift 2
if [ "$1" != "--" ]; then
  echo "missing --" >&2
  exit 2
fi
shift

mkdir -p "$out"
: > "$out/docker-stats.ndjson"
date -u +%Y-%m-%dT%H:%M:%SZ > "$out/started-at.txt"

for c in $containers; do
  docker inspect "$c" --format '{{json .HostConfig}}' > "$out/$c-hostconfig.json"
  docker exec "$c" sh -c 'cat /sys/fs/cgroup/cpu.stat; printf "memory.current "; cat /sys/fs/cgroup/memory.current; printf "memory.peak "; cat /sys/fs/cgroup/memory.peak' > "$out/$c-cgroup-before.txt"
done

(
  while :; do
    ts=$(date -u +%Y-%m-%dT%H:%M:%S.%NZ)
    for c in $containers; do
      docker stats --no-stream --format '{{json .}}' "$c" | sed "s/^{/{\"sampledAt\":\"$ts\",/"
    done
    sleep 1
  done
) >> "$out/docker-stats.ndjson" 2>&1 &
sampler=$!

set +e
"$@" > "$out/command.stdout" 2> "$out/command.stderr"
status=$?
set -e

kill "$sampler" 2>/dev/null || true
wait "$sampler" 2>/dev/null || true

for c in $containers; do
  docker exec "$c" sh -c 'cat /sys/fs/cgroup/cpu.stat; printf "memory.current "; cat /sys/fs/cgroup/memory.current; printf "memory.peak "; cat /sys/fs/cgroup/memory.peak' > "$out/$c-cgroup-after.txt"
done
date -u +%Y-%m-%dT%H:%M:%SZ > "$out/finished-at.txt"
echo "$status" > "$out/exit-code.txt"
exit "$status"
