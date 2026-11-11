#!/bin/sh
set -eu

if [ "$#" -ne 6 ]; then
  echo "usage: $0 ENGINE ADDRESS REST USER PASSWORD 'containers'" >&2
  exit 2
fi

engine=$1
address=$2
rest=$3
user=$4
password=$5
containers=$6
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)

for pass in 1 2; do
  for clients in 10 50 100; do
    out="$root/benchmark/raw/$engine/load-${clients}-pass${pass}"
    echo "START engine=$engine clients=$clients pass=$pass $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    "$root/benchmark/scripts/measure-command.sh" "$out" "$containers" -- \
      "$root/benchmark/bin/bench" -mode load -engine "$engine" -address "$address" -rest "$rest" \
      -user "$user" -password "$password" -clients "$clients" -workers 8 -batch 32 \
      -warmup 10s -duration 60s -output "$out/result.json"
    echo "DONE engine=$engine clients=$clients pass=$pass $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  done
done
