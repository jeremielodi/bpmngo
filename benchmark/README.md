# BpmnGo ⇄ Camunda 8.10.7 benchmarks

Two tools, both driving the engines only through the public Zeebe gRPC
protocol and the Camunda 8 REST `/v2` API, as a Camunda 8 application does:

| What | Tool | Where |
|---|---|---|
| **Behaviour** (62 scenarios: gateways, events, jobs, messages, timers, multi-instance, FEEL, API checks), with a per-section diff against Camunda | Node/TypeScript | [`compare/`](compare/README.md): `npm run compare` |
| **Load** (closed-loop clients with the SDKs' back-off, fixed-rate "highest sustained rate", repeats with mean ± standard deviation, CPU per instance) | Node/TypeScript | [`compare/`](compare/README.md): `npm run load` |
| **Latency per call** and **endurance** with PostgreSQL internals (`pg_stat_statements`, WAL, I/O, bloat) | Go | `cmd/bench` (`-mode latency` / `-mode load`), `scripts/run-endurance.sh` |

`compare/` replaced the Go behaviour suite (47 scenarios) on 2026-11-03: it
covers more, reports what differs rather than a count, and found the
join and multi-instance defects the Go suite missed. Camunda 8.10.7's traces
of its suite are in `compare/reference-camunda-8.10.7/`, so BpmnGo can be
checked without running Camunda.

Camunda 8.10.7 itself runs from `camunda-8.10.7/` (create its `.env`:
`CAMUNDA_VERSION=8.10.7`, `CAMUNDA_CONNECTORS_VERSION=8.10.2`).

**Same capacity for both engines**: two *physical* cores and 2 GiB each.
`compare/limits/` takes the cores from `BENCH_CAMUNDA_CPUS` /
`BENCH_BPMNGO_CPUS`. The older overrides here (`bpmngo.override.yaml`,
`camunda-8.10.7/docker-compose.override.yaml`) pin CPUs `0-1`: on a
hyperthreaded machine that is one physical core, shared with the host's
interrupts. Check the topology first:
`docker run --rm alpine cat /sys/devices/system/cpu/cpu*/topology/thread_siblings_list`.

## Reports (history)

The completed report is [`REPORT.md`](REPORT.md), with consolidated measurements
in [`raw/summary.json`](raw/summary.json). The Go harness is in `cmd/bench` and
the measurement scripts are in `scripts`.

Post-benchmark compatibility corrections and their 47/47 verification are
documented in [`IMPROVEMENTS.md`](IMPROVEMENTS.md).

The complete post-improvement performance rerun is documented in
[`REPORT-RERUN-20261029.md`](REPORT-RERUN-20261029.md), with raw data under
[`raw/rerun-20261029`](raw/rerun-20261029).

The PostgreSQL tuning and direct-BIGINT campaigns are documented in
[`REPORT-POSTGRESQL-TUNING-20261029.md`](REPORT-POSTGRESQL-TUNING-20261029.md)
and
[`REPORT-ENDURANCE-BIGINT-POSTGRESQL-20261029.md`](REPORT-ENDURANCE-BIGINT-POSTGRESQL-20261029.md).

The archive contention correction (dynamic foreground admission, one CPU
reserved for PostgreSQL, grouped archive transactions and reduced audit WAL)
is measured in
[`REPORT-ARCHIVER-ADMISSION-BATCHING-20261029.md`](REPORT-ARCHIVER-ADMISSION-BATCHING-20261029.md).

The consolidated current-system comparison, including the strict A/B results
and the later BpmnGo-only PostgreSQL improvements, is
[`REPORT-CURRENT-BPMNGO-VS-CAMUNDA-20261029.md`](REPORT-CURRENT-BPMNGO-VS-CAMUNDA-20261029.md).

The endurance runner captures Docker resources plus `pg_stat_wal`,
`pg_stat_bgwriter`, `pg_stat_io`, table bloat and `pg_stat_statements`:

```bash
./benchmark/scripts/run-endurance.sh <run-id> <grpc-address> <rest-url> \
  <user> <password> '<app-container> <postgres-container>' 30m 100 \
  <postgres-container>
```
