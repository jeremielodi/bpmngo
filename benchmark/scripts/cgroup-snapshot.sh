#!/bin/sh
set -eu

for name in "$@"; do
  echo "container=$name"
  docker exec "$name" sh -c 'cat /sys/fs/cgroup/cpu.stat; printf "memory.current "; cat /sys/fs/cgroup/memory.current; printf "memory.peak "; cat /sys/fs/cgroup/memory.peak' 2>/dev/null || \
    docker exec "$name" bash -c 'cat /sys/fs/cgroup/cpu.stat; printf "memory.current "; cat /sys/fs/cgroup/memory.current; printf "memory.peak "; cat /sys/fs/cgroup/memory.peak'
done
