#!/bin/sh
set -eu

if [ "$#" -lt 1 ]; then
  echo "usage: $0 CONTAINER..." >&2
  exit 2
fi

for name in "$@"; do
  docker inspect "$name" --format '{{.Name}} cpuset={{.HostConfig.CpusetCpus}} memory={{.HostConfig.Memory}} image={{.Config.Image}}'
done
