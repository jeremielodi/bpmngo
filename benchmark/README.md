# BpmnGo ⇄ Camunda 8 benchmark

Runs the same BPMN scenarios on **Camunda 8** and on **BpmnGo**, with the
same client code, and reports every difference in behaviour. The same tool
measures them under load (`npm run load`, with PostgreSQL's own figures
for BpmnGo) and call by call (`npm run latency`).

| Command | What |
|---|---|
| `npm run compare` | behaviour: 88 scenarios + 5 API checks, every difference shown section by section |
| `npm run load` | load: clients and fixed-rate levels, CPU and memory per instance, BpmnGo's PostgreSQL statements, commits and WAL per instance; a long `--duration` is an endurance run |
| `npm run latency` | one call at a time, p50/p95 per call, two passes |
| `npm run experiment` | `experiments/exclusive-order.ts`: which flow an exclusive gateway takes when several conditions are true |

Both engines are driven exactly as a Camunda 8 application drives them:

- the **Zeebe gRPC API** (official `gateway.proto`, from the Apache-2.0
  `@camunda8/sdk` package, in `proto/zeebe.proto`) to deploy, start
  instances, work jobs, publish messages and broadcast signals;
- the **`/v2` REST API** to complete user tasks and to read the end state
  (instance state, active elements, open incidents).

For each scenario the bench records what a client can observe: which jobs
arrive, in which order and with which variables; the answer to every call;
the outcome; the final variables; the end state. Then it compares Camunda
(the reference) with BpmnGo.

## Latest results

**2026-11-03, BpmnGo `main` (PostgreSQL 18) against a fresh Camunda 8.10.7,
both run live, each on two physical cores and 2 GiB: the 87 checks the suite
then had all behaved the same.** Their traces are `reference-camunda-8.10.7/`.

**Added 2026-11-04** — six scenarios for the shapes the suite was missing: a
realistic model of 72 elements (`large.bpmn`: four-way parallel fork in a
sub-process, join, script task, default-branch gateway, sequential
multi-instance, nested audit sub-process), user tasks carrying a form,
candidate groups and a priority, and two chained DMN tables with FIRST and
UNIQUE hit policies. Each was run against a live Camunda 8.10.7 and BpmnGo:
6 of 6 the same. The suite is now **88 scenarios + 5 API checks**; the full
93 have not yet been re-run in a single pass, and `reference-camunda-8.10.7/`
still holds the 87-check traces.

The 25 scenarios of the *Robustness* group cover the shapes real models
are made of: loops back to a gateway; sub-processes nested, interrupted by
an error, a timer or a terminate end, raising escalations and error end
events; five and nested parallel branches; inclusive joins; link events;
several end events; FEEL script tasks; DMN decisions; 200 KB, nested and
non-ASCII variables; event-based gateways; correlation on a nested key;
multi-instance sub-processes with a gateway inside; a non-interrupting
boundary firing twice; a call activity waiting on a timer; and models as
Camunda Modeler exports them (diagram, documentation, properties).

They found two defects, fixed the same day: `zeebe:taskDefinition retries`
was ignored (every job started with 3), and the jobs of a parallel
multi-instance were announced one at a time instead of in one batch.

Over the whole suite, BpmnGo used 0.27× Camunda's CPU time and 0.22× its
peak memory, and ran it in 0.81× the time. Most of that is *idle footprint* —
the scenarios wait on real timers — not per-instance efficiency: under load,
BpmnGo spends 39–45 ms of CPU per finished instance against Camunda's
47–60 ms (`npm run load`, see below).

The exclusive gateway with several conditions true follows Camunda: the
**last** true flow, in the order of the `sequenceFlow` elements of the file,
whatever the gateway's `<outgoing>` list says (`npm run experiment`; its
documentation says "the first"). `EXCLUSIVE_GATEWAY_ORDER=bpmn` keeps the
BPMN 2.0 rule instead.

This bench found the parallel/inclusive join and multi-instance defects
fixed in 8adbdd5 (the 2026-10-30 runs), which the earlier Go behaviour suite
did not cover.

Older figures (2026-10-29, BpmnGo 1.0.1) are in the git history of this
file. Figures measured with `limits/` before 2026-11-03 gave each engine
CPUs `2,3` / `4,5`, a single physical core on hyperthreaded machines:
see "Same capacity for both engines".

---

## 1. Requirements

- Node.js 20 or later.
- Docker (to run Camunda 8, and BpmnGo if you use its compose stack).

## 2. Start BpmnGo on other ports

Camunda and BpmnGo both use ports **8080** and **26500**. From the
repository root:

```shell
SERVER_PORT=9080 GRPC_PORT=26600 docker compose up -d postgres bpmngo
```

BpmnGo then answers on `localhost:9080` (REST) and `localhost:26600` (gRPC).
The bench logs in with `admin@bpmngo.com` / `admin123` (HTTP Basic, on both
gRPC and REST); change it with `--bpmngo-user` and `--bpmngo-password`.

**BpmnGo's PostgreSQL must run in UTC.** The `postgres:18` image of the
compose stack does. A PostgreSQL in another time zone makes every BpmnGo
timer fire at the wrong moment (see the `api-timer-waits-its-duration` check).

**REST keys.** `/v2` answers with the int64 keys gRPC returns, as strings,
as Camunda 8 does (BpmnGo's default, `REST_KEYS=int`). With `REST_KEYS=uuid`
the `api-grpc-key-usable-in-rest` check differs from Camunda's.

**FEEL mode.** BpmnGo's default is `FEEL_MODE=shadow` (expressions evaluated
by CEL). Camunda uses FEEL. To compare BpmnGo's native FEEL engine too, run
the bench a second time with BpmnGo started with `FEEL_MODE=feel`.

## 3. Start Camunda 8

`camunda-8.10.7/` holds Camunda's own lightweight distribution (Orchestration
and Connectors). Create its `.env` first (not in git):

```shell
cd camunda-8.10.7
printf 'CAMUNDA_VERSION=8.10.7
CAMUNDA_CONNECTORS_VERSION=8.10.2
' > .env
docker compose -f docker-compose.yaml -f ../limits/camunda.yaml up -d
```

Or the official distribution:

Version 8.10 (the bench also works with 8.8 and later):

```shell
curl -LO https://github.com/camunda/camunda-distributions/releases/download/docker-compose-8.10/docker-compose-8.10.zip
unzip docker-compose-8.10.zip -d camunda-8.10
cd camunda-8.10
docker compose up -d
```

Wait until `docker compose ps` shows every service healthy (a few minutes).
Camunda then answers on `localhost:8080` (REST, Operate at `/operate`,
login `demo` / `demo`) and `localhost:26500` (gRPC). In this setup the APIs
need no credentials.

To start from a clean Camunda: `docker compose down -v`, then `up -d` again.

## 4. Run

```shell
npm install
npm run compare
```

A full run takes about 15 minutes (two engines, ~8 s per scenario: timers
and waits are real). At the end the summary is printed and everything is in
`results/<date>/`:

```
results/2026-10-29T10-00-00/
  report.txt               the report: one line per scenario, then the details of each difference
  summary.json             the same verdicts, machine-readable
  resources.json           CPU and memory of each engine's containers during its run
  traces/camunda/*.json    what Camunda did, scenario by scenario
  traces/bpmngo/*.json     what BpmnGo did
```

The command exits with code 1 when there is at least one difference.

## Options

```
--camunda-grpc  host:port   Camunda gRPC gateway       (default localhost:26500)
--camunda-rest  url         Camunda REST API           (default http://localhost:8080)
--camunda-user / --camunda-password   Basic credentials for Camunda, if needed

--bpmngo-grpc   host:port   BpmnGo gRPC gateway        (default localhost:26600)
--bpmngo-rest   url         BpmnGo REST API            (default http://localhost:9080)
--bpmngo-user / --bpmngo-password     default admin@bpmngo.com / admin123

--engines camunda,bpmngo    which engines to run       (default both)
--only <text>               only scenarios whose name or group contains <text>
                            (e.g. --only gw-   --only Messages   --only api)
--skip-api-checks           skip the API checks
--out <dir>                 output folder              (default results/<date>)
--reference <dir>           compare BpmnGo with Camunda traces recorded earlier
                            (a traces/camunda folder) instead of running Camunda
--compare <ref dir> <dir>   compare two saved trace folders, run nothing

--camunda-containers a,b    Docker containers measured for Camunda (default orchestration)
--bpmngo-containers a,b     Docker containers measured for BpmnGo
                            (default bpmngo-app,bpmngo-postgres,bpmngo-redpanda)
--no-resources              do not measure CPU and memory
```

Options that take a value can also be given as environment variables:
`BPMNGO_GRPC`, `CAMUNDA_REST`, `ONLY`, …

## 5. Reading the report

```
── Gateways ──────────────────────────────────────────────────────────
  SAME       gw-exclusive-default-taken
  DIFFERENT  gw-exclusive-condition-on-missing-variable   (outcome, instances)
```

- **SAME**: both engines did exactly the same thing.
- **DIFFERENT**: something differs; the details section shows only what
  differs, Camunda first:

  ```
  ▶ boundary-timer-interrupting  [different]
    · jobs
        [0] be-slow @ ti_slow
          result:
            camunda: NOT_FOUND
            bpmngo : FAILED_PRECONDITION
  ```

- **EXPECTED**: a difference listed in `known-differences.json` (see below).
- **MISSING**: one engine has no trace for that scenario.

The compared sections of a scenario:

| Section          | What it is |
|------------------|------------|
| `deploy`, `start`| `ok`, or the gRPC error code |
| `outcome`        | `completed`, `not completed within Ns` (incident, or still waiting), `no result requested` (instances started by a message or a signal) |
| `finalVariables` | the instance's variables at the end (from `CreateProcessInstanceWithResult`) |
| `jobs`           | every job handed out: element, type, retries, headers, the variables it carried, what the worker did, and the engine's answer (`ok` or a gRPC code, e.g. `NOT_FOUND` for a job cancelled meanwhile) |
| `actions`        | each message/signal sent by the scenario and the engine's answer |
| `instances`      | from the REST search at the end: state, active elements and open incidents of every instance of the scenario's processes |

Error messages and timings are shown under "notes" but never compared:
their wording always differs between engines.

Jobs of parallel branches can arrive in any order; for those scenarios the
jobs are compared sorted.

### CPU and memory

While each engine plays its scenarios, the bench measures its Docker
containers and adds a section to the report, after the verdicts:

```
── Resources (Docker) ────────────────────────────────────────────────
  container                  run  CPU time  CPU avg  CPU peak  mem idle  mem avg  mem peak  mem limit
  camunda (total)          200 s   60.1 s     30 %     180 %   820 MiB  840 MiB   870 MiB
    orchestration                  60.1 s     30 %     180 %   820 MiB  840 MiB   870 MiB   1024 MiB
  bpmngo (total)           190 s   12.3 s      6 %      40 %   260 MiB  270 MiB   290 MiB
    bpmngo-app                      ...
```

- **CPU time** is exact: each container's cgroup counter, read before and
  after the run. **CPU avg** is that time divided by the run time; CPU is in
  % of one core (200 % = two cores busy).
- **CPU peak** and **memory** come from `docker stats`, sampled back to back
  (one sample every ~2 s); **mem idle** is the sample taken just before the
  run. A total's peak is the highest combined value seen in one sample.
- Each engine is measured during its own run, so the other engine is idle.
- Count every container an engine needs to run: BpmnGo needs its database
  and Redpanda, Camunda's `orchestration` container holds its own storage.
  The connectors and the frontend are left out because the bench does not
  use them.
- The memory limit matters: a JVM sizes its heap from it. Compare the
  engines with the limits they would have in production.

Needs the `docker` command on the machine running the bench. Without it,
or with `--no-resources`, the section is left out.

### Same capacity for both engines

Out of the box, Camunda's compose file caps `orchestration` at 1 GiB and
BpmnGo has no limit at all, so CPU and memory figures are not comparable.
`limits/` holds two compose override files that give each engine the same
budget: **2 dedicated CPUs and 2 GiB of memory in all**.

| Engine  | CPUs (pinned cores)          | Memory |
|---------|------------------------------|--------|
| Camunda | `BENCH_CAMUNDA_CPUS` (2,4) — `orchestration` | 2 GiB |
| BpmnGo  | `BENCH_BPMNGO_CPUS` (6,8) — shared by its containers | `bpmngo.yaml`: 512 MiB app + 512 MiB Postgres + 1 GiB Redpanda; `bpmngo-direct.yaml` (default direct mode): 1 GiB + 1 GiB |

The cores are set by `BENCH_CAMUNDA_CPUS` (default `2,4`) and
`BENCH_BPMNGO_CPUS` (default `6,8`). Give each engine two *physical*
cores, and not the same ones: with hyperthreading, logical CPUs 2 and 3 are
often one core. Check the machine's topology first:

```shell
docker run --rm alpine cat /sys/devices/system/cpu/cpu*/topology/thread_siblings_list
```

Pinning (`cpuset`) rather than a CPU quota lets BpmnGo's three containers
share their two cores as they need; the bench itself runs on the other
cores. Apply them from each engine's folder (the containers are recreated,
the data stays in the volumes):

```shell
# benchmark/camunda-8.10.7
docker compose -f docker-compose.yaml -f ../limits/camunda.yaml up -d orchestration
# repository root (BpmnGo's default direct mode: no Kafka)
SERVER_PORT=9080 GRPC_PORT=26600 docker compose -f docker-compose.yaml -f benchmark/limits/bpmngo-direct.yaml up -d postgres bpmngo
```

A later `docker compose up -d` without the `-f` files drops the limits
again. The report's last column shows each container's limits, so a run
without them is easy to spot. The cores and sizes are only a starting
point: edit both files the same way to test another budget, and keep at
least 2 GiB for Camunda.

`limits/camunda.yaml` also sets `JAVA_TOOL_OPTIONS=-XX:MaxRAMPercentage=70`:
without it the JVM sizes its heap at 25 % of the 2 GiB limit (512 MiB) while
BpmnGo's Go runtime may use most of its 1 GiB, so the budgets would not be
equal in practice. It changes nothing measurable (23.3 vs 22.8 inst/s at 25
offered) and removes the objection.

`limits/camunda-tuned.yaml` is the "is the gap the engine, or the default
configuration?" question: 70 % of the memory as heap, and `cpu-thread-count` /
`io-thread-count` set to 2 for a 2-core container instead of the
distribution's 3. Stack it on the other two:

```shell
BENCH_CAMUNDA_CPUS=2,4 docker compose -f docker-compose.yaml -f ../limits/camunda.yaml \
  -f ../limits/camunda-2p.yaml -f ../limits/camunda-tuned.yaml up -d orchestration
```

Measured that way Camunda still tops out around 21–23 inst/s: the ceiling is
its stream processor and exporter, not memory. Publish both figures, or the
answer to "you did not configure Camunda properly" is left open.

### Load test

The scenarios check behaviour one instance at a time. `npm run load` puts
both engines under the same sustained load:

```shell
npm run load                                  # levels 10, 50, 100 clients, 60 s each
npm run load -- --rates 5,10,15,20,25         # fixed rates, in instances started per second
npm run load -- --levels 10,50,100 --rates 5,10,15,20,25 --repeat 3
```

- **Workload**: one process, start → two service tasks → end, with a few
  business variables. The same workers (8 per job type, batches of 32)
  complete the jobs on both engines. With `--process bpmn/large.bpmn` the
  generated process is replaced by a real model, deployed as it is: its job
  types are read from the file and each gets `--workers` workers, so the
  parser and deployment cost of a realistic model is paid too.
- **Clients levels** (`--levels`): that many clients each start an
  instance with `CreateProcessInstanceWithResult`, wait for its end, and
  start the next. A refused client waits before trying again: 0–100 ms,
  doubling up to 5 s, with jitter, as the official SDKs do. Without that
  wait, refused clients would come straight back and the test would punish
  Camunda's back-pressure for a behaviour its real clients do not have.
- **Rate levels** (`--rates`): starts are sent at a fixed rate, whatever
  the engine answers, and a refused start is not retried. A rate is
  *sustained* when no start was refused, at least 98 % of the instances
  completed and the p99 stays under `--p99-limit` (1000 ms). The report
  gives the highest rate each engine sustains **and, level by level, why the
  others were not**: refused (the engine pushes back) or queued (it accepts
  everything and the p99 grows). One column could not tell those apart.
- **Measured**, after a warm-up (10 s per level, `--first-warmup` 60 s on the
  first one, so the cold start — JVM class loading and JIT — is not counted),
  over the measured window (60 s): instances per second, end-to-end latency
  (p50, p95, p99), errors by gRPC code (`RESOURCE_EXHAUSTED` is back-pressure,
  `DEADLINE_EXCEEDED` a start that got no answer within 40 s), CPU and
  memory of the engine's containers, and the CPU time per completed instance.
  An instance counts only if it *started and finished inside* the window:
  completions that land after it are the backlog the engine was still
  working through, reported apart, not credited as throughput.
- **Repeats** (`--repeat 3`): the whole test runs that many times, the
  engine order alternating; the report gives mean±standard deviation, and
  each run.
- **Before measuring**:
  - apply `limits/` so both engines have the same capacity (the load report
    does not show the limits, the main bench does);
  - give Camunda **2 partitions** with `limits/camunda-2p.yaml`. Zeebe runs
    each partition on one thread: with the compose file's single partition
    it uses about one of its two cores, while BpmnGo uses both. The override
    mounts new volumes, the existing data stays as it is. Check
    `partitionsCount` in `curl localhost:8080/v2/topology`;
  - stop Camunda's `connectors` container (`docker stop connectors`): it is
    a second JVM that the workload does not use. Only `orchestration` is
    counted in Camunda's CPU and memory.
- **PostgreSQL (BpmnGo)**: over each measured window, the differences of
  `pg_stat_statements`, `pg_stat_wal` and `pg_stat_database` give, per
  completed instance, the statements run, their execution (and, with
  `pg_stat_statements.track_planning=on`, planning) time, the commits and
  the WAL written, and the five heaviest statements. Read with `docker exec
  … psql` in the measured container named `*postgres*` (`--bpmngo-postgres`
  to choose it, `--pg-user` / `--pg-db`, default `goflow`; `--no-pg-stats`
  to skip). This is where BpmnGo's CPU goes: Camunda has no equivalent, its
  state is in its own process.
- **Endurance**: one level with a long window, e.g.
  `npm run load -- --engines bpmngo --levels 100 --duration 1800`.
- Output: `results/load-<date>/load-report.txt` and `load.json`.

```
--levels 10,50,100   clients levels               --rates 5,10,20  fixed-rate levels (inst/s)
--duration 60        measured seconds per level   --warmup 10      warm-up seconds per level
--first-warmup 60    warm-up seconds on level 1: absorbs the engine's cold start (JVM class
                     loading and JIT), which used to land in level 1's CPU and latency columns
--process bpmn/large.bpmn   run a real model instead of the generated start → 2 tasks → end:
                     its job types are read from the file, and its parser/deploy cost is paid
--repeat 1           runs, engine order alternating --p99-limit 1000 p99 (ms) for a sustained rate
--workers 8          workers per job type
--bpmngo-postgres c   PostgreSQL container read   --no-pg-stats    skip PostgreSQL's figures
--engines, --*-grpc, --*-user, --*-containers, --no-resources, --out: as for npm run compare
```

### Latency per call

`npm run latency` makes each call alone, one after the other, without load:
Topology, DeployResource, CreateProcessInstance, CreateProcessInstanceWithResult
(start → end), creation → job (until ActivateJobs hands it out), CompleteJob.
`--iterations 30` calls of each kind per pass, `--repeat 2` passes (engine
order alternating), `--warmup 5` unmeasured rounds before each pass — without
them the JVM's first calls, ~5× slower, were reported as the p50. Output in
`results/latency-<date>/`.

Limits of the result: one client machine, one short process (two service
tasks; no timers, messages, gateways or large variables), Camunda's
back-pressure left at its defaults (AIMD). The figures hold for this kind
of process on this hardware, not for a tuned cluster.

## 6. Known differences

When a difference is understood and accepted, list it in
`known-differences.json`; it then shows as EXPECTED instead of DIFFERENT:

```json
[
  { "scenario": "boundary-timer-interrupting", "section": "jobs",
    "note": "late completion of a cancelled job: BpmnGo answers FAILED_PRECONDITION" },
  { "scenario": "*", "section": "instances",
    "note": "incident types use Camunda 7 names in BpmnGo" }
]
```

`"scenario": "*"` applies to every scenario. Re-check without re-running:

```shell
npm run compare -- --compare results/<date>/traces/camunda results/<date>/traces/bpmngo
```

## 7. Record Camunda once, compare BpmnGo in CI

`reference-camunda-8.10.7/traces/camunda/` holds Camunda 8.10.7's traces of
this suite: compare BpmnGo against them without running Camunda:

```shell
npm run compare -- --engines bpmngo --reference reference-camunda-8.10.7/traces/camunda
```

Record them again after upgrading Camunda, or after changing a scenario:

```shell
npm run compare -- --engines camunda --out reference-8.10
```

commit `reference-8.10/traces/camunda/`, and in CI compare BpmnGo against
them, without running Camunda:

```shell
npm run compare -- --engines bpmngo --reference reference-8.10/traces/camunda
```

## 8. Adding a scenario

A scenario is a BPMN file in `bpmn/` plus an entry in `scenarios/*.ts`:

```ts
{
  name: 'gw-exclusive-default-taken',          // unique, used as file name
  group: 'Gateways',
  description: 'No condition true: the default flow is taken.',
  bpmn: ['gateways.bpmn'], process: 'gw_exclusive',
  variables: { amount: 50 },                   // start variables
  workers: {                                   // job type -> what the worker does
    'gw-small': () => ({ complete: { done: true } }),
    'gw-big':   v => ({ error: { code: 'NOPE' } }),
    'gw-slow':  () => ({ later: 4000, then: { complete: {} } }),  // answer after 4 s
  },
  actions: [                                   // what the outside world does
    { afterJob: 'gw-small', publish: { name: 'paid', correlationKey: 'A1' } },
    { afterMs: 2000, signal: { name: 'stop' } },
  ],
  unordered: true,                             // parallel branches: compare jobs sorted
}
```

Worker answers: `complete`, `fail` (retries, backoff, variables), `error`
(BPMN error code), `ignore`, `later`. Actions: `publish`, `signal`,
`setVariables`, `cancel`, `completeUserTask`.

Every run renames process ids, job types, message and signal names with a
per-run suffix, so leftovers of earlier runs never interfere; traces store
the original names.

Two traps, both found while writing the `Large model` group:

- **Do not give two parallel branches the same job type.** Jobs are activated
  per type, and whether both arrive in one batch decides which variables they
  carry: Camunda snapshots them at activation, so the second job sees only the
  variables of the moment, while an engine that activates it later carries
  the first branch's results. The trace then differs for a reason that
  belongs to the harness, not to the engine.
- **A `zeebe:formDefinition` whose form is not deployed gives Camunda an
  incident** (`FORM_NOT_FOUND`, task stuck in ACTIVE) while BpmnGo completes
  the task. Deploy the form with the model, and use `schemaVersion: 5` in it:
  with `schemaVersion: 1` Camunda accepts the deployment and still cannot
  resolve the form at runtime.

A model written by hand also needs its `<bpmndi:BPMNDiagram>` section: the
engines run it without one, but a modeler (BpmnGo's `/modeler`, Camunda
Modeler) has nothing to draw and opens it empty. Several processes in one
file go in a `bpmn:collaboration`, one pool each (see `gateways.bpmn`); the
content of a sub-process can be drawn in its own drill-down diagram (see
`large.bpmn`).

## 9. Scope and limits

- 88 scenarios, plus 5 API checks (gRPC key usable in REST,
  `/v2/element-instances/search`, searching finished instances, deploy
  response, timer accuracy).
- Not the web UIs, not the Camunda 7 REST API (that needs a Camunda 7 next
  to it).
- How it was validated before delivery: every scenario was run twice on
  BpmnGo with identical traces; every BPMN model passes Camunda's own
  compatibility rules for 8.10 (`bpmnlint-plugin-camunda-compat`). The
  Camunda side could not be run where the bench was written: if a scenario
  misbehaves on Camunda for a reason unrelated to the engines, the trace
  notes show the gRPC/HTTP error.
