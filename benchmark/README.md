# Camunda 8.10.7 vs BpmnGo 1.0.1 benchmark

This directory contains the reproducible harness and raw evidence for the
comparison requested in `PROMPT-AUTRE-IA.md`.

The benchmark uses only the public Zeebe gRPC protocol and Camunda 8 REST
`/v2` endpoints.  Compose overrides assign both engines CPUs `0-1` and a
combined memory limit of 2 GiB.

Generated measurements are written below `raw/<run-id>/`; the final report is
`REPORT.md`.

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
