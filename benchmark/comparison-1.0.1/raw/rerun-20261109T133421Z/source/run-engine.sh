#!/bin/sh
set -eu
engine=$1
base=/local-workspace/bpmngo/benchmark/comparison-1.0.1
out="$base/raw/rerun-20261109T133421Z/$engine"
bench=/local-tmp/bpmngo-rerun-bench
mkdir -p "$out"
case "$engine" in
  camunda) address=127.0.0.1:26500; rest=http://127.0.0.1:8080; user=; password=; containers='orchestration connectors' ;;
  bpmngo) address=127.0.0.1:26600; rest=http://127.0.0.1:9080; user=admin@goflow.com; password=admin123; containers='bpmngo-benchmark-app bpmngo-benchmark-postgres' ;;
  *) exit 2 ;;
esac
verify() {
  target=$1
  mkdir -p "$target"
  docker ps --format '{{.Names}} {{.Image}}' > "$target/active-containers.txt"
  for running in $(docker ps --format '{{.Names}}'); do
    case " $containers " in *" $running "*) ;; *) echo "Unrelated running container: $running" >&2; exit 3 ;; esac
  done
  for container in $containers; do
    docker inspect "$container" --format '{{.Name}} {{.HostConfig.CpusetCpus}} {{.HostConfig.Memory}}' >> "$target/limits.txt"
  done
  awk '{ if ($2 != "0-1") exit 1; sum += $3; count++ } END { if (count != 2 || sum != 2147483648) exit 1 }' "$target/limits.txt"
  docker stats --no-stream --format '{{json .}}' $containers > "$target/idle-stats.ndjson"
}
call() {
  "$bench" -engine "$engine" -address "$address" -rest "$rest" -user "$user" -password "$password" "$@"
}
measure() {
  name=$1
  shift
  if [ -f "$out/$name/exit-code.txt" ] && [ "$(cat "$out/$name/exit-code.txt")" = 0 ]; then
    echo "PRESERVED $engine $name"
    return
  fi
  echo "START $engine $name $(date -u +%FT%TZ)"
  verify "$out/$name"
  "$base/scripts/measure-command.sh" "$out/$name" "$containers" -- "$bench" -engine "$engine" -address "$address" -rest "$rest" -user "$user" -password "$password" -output "$out/$name/result.json" "$@"
  jq -c '{engine,clients,instances_per_second,errors,scenarios:(if .scenarios then (.scenarios|length) else null end)}' "$out/$name/result.json"
  echo "DONE $engine $name $(date -u +%FT%TZ)"
}
if [ ! -f "$out/warmup.json" ]; then
  verify "$out/preflight"
  call -mode probe -output "$out/probe.json"
  echo "WARMUP $engine $(date -u +%FT%TZ)"
  call -mode load -clients 10 -workers 8 -batch 32 -warmup 0s -duration 2m -output "$out/warmup.json"
fi
measure behavior -mode behavior
measure behavior-pass2 -mode behavior
for pass in 1 2; do
  measure "latency-pass$pass" -mode latency -iterations 30
done
for pass in 1 2; do
  for clients in 10 50 100; do
    measure "load-$clients-pass$pass" -mode load -clients "$clients" -workers 8 -batch 32 -warmup 10s -duration 60s
  done
done
echo "ENGINE_COMPLETE $engine $(date -u +%FT%TZ)"
