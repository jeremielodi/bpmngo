# BPMN simulation laboratory

The **Simulation studio** organizes work into saved projects containing one
or more BPMN models, model-element constraints, handoffs, and a workload.
It reuses BpmnGo's BPMN parser and
graph builder, while keeping the simulation runtime independent from the
transactional runtime.

## What it covers

- save empty or unfinished projects, upload multiple BPMN files, create/edit
  models in the modeler and save directly back into the project;
- click diagram elements to edit processing/wait distributions, fixed cost,
  resource pools, skills, priorities, routing and branch probabilities;
- explicit token handoffs between model elements with transit delay and cost;
  model-local IDs are namespaced so files can reuse the same XML element IDs;
- as-run path counts, case replay, bottleneck highlighting, ranked waiting
  metrics and execution-log export using the run's original model snapshot;
- expected visits per activity, including probabilistic loops;
- processing time, explicit waiting time, cycle time, value-added time,
  cycle-time efficiency, and labor cost per execution;
- Erlang-C (`M/M/c`) capacity planning for a target mean queueing time;
- seeded discrete-event simulation with Poisson arrivals, FIFO resource
  queues, finite resource capacity, and fixed, normal, exponential, uniform,
  or triangular duration distributions;
- constant, piecewise time-varying, scheduled, and batched arrival patterns;
- timezone-aware weekly business calendars, capacity shifts, and unavailable
  exception dates; work pauses outside configured availability;
- skill-based eligible resource pools, fixed/least-queue/round-robin routing,
  and priority queues with FIFO ordering inside the same priority;
- structured parallel and inclusive fork/join gateways, including nested
  forks and inclusive branches selected independently;
- asynchronous, resumable campaigns with progress, cancellation, and a
  configurable PostgreSQL-backed worker pool;
- automatic resource-capacity sweeps with target-based recommendations;
- full-factorial, multi-parameter experiments (up to 256 combinations) over
  demand, duration, resource capacity/cost, task durations/priorities, and
  gateway probabilities, with feasibility constraints and an optimization
  objective;
- mean, median, p90 and p95 cycle time, throughput, cost, resource utilization,
  queues, and 95% confidence intervals across replications;
- an evidence-based issue register, redesign heuristics, and as-is/to-be
  comparison.
- immutable scenario revisions with non-destructive restore, portable JSON
  export/import, and a dashboard comparing any two completed simulation runs.
- previewable calibration from archived BpmnGo instances: aggregate arrival
  rates, repeating 24-hour demand profiles when enough history exists,
  transition durations, and separate task claim/processing waits;
- embedded subprocess traversal, probabilistic event-based gateway races,
  atomic call-activity surrogates, and probabilistic interrupting boundary
  alternatives in addition to the existing gateway semantics.

The studio starts with a neutral scenario rather than a domain-specific
workflow. Importing a `.bpmn` file discovers tasks and configurable gateway
flows automatically, initializes safe placeholder assumptions, and preserves
matching parameters when the model is replaced by a newer revision. The
built-in **Support operations** scenario is an optional product example, not
a required workflow or a special execution mode.

## Workflow

1. Open **Simulation studio → Project & models**, name and save a project.
   Upload BPMN files or choose **Create in modeler → Save to project**.
2. Select a model and click tasks/events/flows to edit constraints directly.
   A top-level element can hand its token to a selected element in another
   model. A handoff replaces the source's normal continuation and keeps the
   same case identity. Define transport delays and costs on the handoff.
   In **Workload & resources**, select the entry model and configure the
   demand pattern, calendars, shifts, resources, skills, routing priorities,
   duration distributions, and explicit waits.
   Exclusive gateway probabilities total `1`; inclusive gateway probabilities
   are independent; parallel gateways need none.
3. Run **Analyze** for deterministic expected values.
4. Use **Capacity planning** to calculate the minimum server count for a
   queueing target.
5. Use **Run experiment** for a persisted campaign. Follow progress, leave
   the page, resume tracking later, or cancel it from **Campaigns**.
   Open the completed run in **Run paths & results**: green paths show actual
   traversals, red elements show the highest observed waits, and purple shows
   the selected case's replay position. Replay follows cases between models.
6. Run a capacity sweep to compare a resource over a range and select the
   smallest capacity meeting the mean/p95 cycle-time targets.
7. Run a multi-parameter experiment to compare combinations and optimize mean
   cycle, p95 cycle, cost, or throughput under optional constraints.
8. In **Calibration**, preview estimates from an archived process-definition
   key. Apply only after reviewing sample counts; application creates a new
   immutable scenario version.
9. Mark the result as the as-is baseline, duplicate it as a to-be scenario,
   change capacity or process parameters, and compare the new result.
10. Track detected and manually observed problems in **Issues**. Use **Modeler**
   to draw the selected to-be process.

## API

All endpoints require authentication. Analysis permissions use
`CAN_VIEW_METRICS`; mutations use the existing modeler permissions.

| Method and path | Purpose |
|---|---|
| `GET/POST /engine-rest/simulation/projects` | List or create projects, including empty drafts |
| `GET/PUT/DELETE /engine-rest/simulation/projects/:id` | Read, version-save, or delete a project |
| `POST /engine-rest/simulation/analyze` | Analyze a scenario without saving it |
| `POST /engine-rest/simulation/simulate` | Simulate a scenario without saving it |
| `POST /engine-rest/simulation/staffing` | Calculate an Erlang-C staffing curve |
| `GET/POST /engine-rest/simulation/scenarios` | List or create scenarios |
| `GET/PUT/DELETE /engine-rest/simulation/scenarios/:id` | Read, update, or delete a scenario |
| `GET /engine-rest/simulation/scenarios/:id/versions` | List immutable saved revisions |
| `POST /engine-rest/simulation/scenarios/:id/versions/:version/restore` | Restore a revision as a new current version |
| `GET /engine-rest/simulation/scenarios/:id/export` | Export a portable scenario package |
| `POST /engine-rest/simulation/scenarios/import` | Import a portable scenario package |
| `POST /engine-rest/simulation/scenarios/:id/calibration` | Preview or apply calibration from historical runtime data |
| `POST /engine-rest/simulation/scenarios/:id/runs` | Queue a persisted simulation |
| `POST /engine-rest/simulation/scenarios/:id/capacity-sweeps` | Queue an automatic capacity comparison |
| `POST /engine-rest/simulation/scenarios/:id/experiments` | Queue a full-factorial multi-parameter experiment |
| `GET /engine-rest/simulation/scenarios/:id/runs` | List prior campaigns |
| `GET /engine-rest/simulation/runs/:runId` | Read a campaign and its result |
| `POST /engine-rest/simulation/runs/:runId/cancel` | Cancel queued/running work |
| `GET/POST /engine-rest/simulation/scenarios/:id/issues` | List or add issues |
| `PUT/DELETE /engine-rest/simulation/issues/:issueId` | Update or remove an issue |

Install migrations `020` through `023` before deploying this version to an
existing database. New databases receive the tables from the baseline schema.
`SIMULATION_WORKERS` controls concurrency (default `2`, maximum `16`).

Projects reuse the existing versioned scenario storage; no additional schema
migration is required. Legacy single-model scenarios remain usable and the
studio converts them to a one-model project when opened. Versions, imports,
exports, calibration, issues, capacity sweeps and factorial experiments include
the whole project configuration. Calibration accepts `modelId` to map historic
local activity IDs to a specific project model.

Each newly queued run atomically captures `scenarioSnapshot` and
`scenarioVersion` in its request. Subsequent project/model edits do not change
that queued run. Results retain the snapshot for diagram replay.

## Modeling assumptions

- Duration units are minutes, costs are hourly, and arrival rates are per hour.
  Element fixed costs and handoff costs are per visit and add to resource labor
  cost. Shared pools model centralized capacity; independent pools and explicit
  handoffs model distributed or federated organizations within one simulation.
  These are simulation interactions, not connections to live external engines.
  Weekly calendar weekdays use ISO numbering (`1` Monday through `7` Sunday).
  Shift and scheduled-arrival offsets are minutes. Calendar exceptions replace
  recurring coverage for their local date; an exception with no shifts is a
  holiday. Shift capacity `0` means the resource's configured capacity.
- Erlang-C models a stationary Poisson arrival process, exponential service,
  one FIFO queue, identical servers, and no abandonment. Simulation queues are
  priority ordered and remain FIFO among jobs with the same priority.
- The simulation drains all cases admitted during the configured arrival
  horizon. Parallel/inclusive gateways must be structured: every selected
  branch must reach a matching gateway of the same type. Business calendars,
  priorities, preemption, and correlated arrivals are not inferred
  automatically. Represent known external delays explicitly.
- Historical calibration is tenant-scoped. Periods shorter than 24 hours
  produce a stationary arrival estimate; longer periods produce a repeating
  hourly profile. Task history separates created-to-claimed waiting from
  claimed-to-completed processing. Other transition elapsed times can still
  contain queue or external wait, so the preview and minimum-sample threshold
  must be reviewed before application.
- Embedded subprocess bodies are traversed directly. Call activities are
  atomic surrogates whose configured duration represents the called process.
  Event-based gateway alternatives use configured probabilities. Interrupting
  boundary paths are probabilistic alternatives; non-interrupting boundary
  events are rejected because they require multi-token lifecycle accounting.
- Recommendations are hypotheses to test in a separate to-be scenario, not
  automatic prescriptions.
- Handoffs currently connect top-level elements, one destination per source.
  Place a decision gateway before several handoff nodes to model alternatives.
  They are one-way continuations; call activities still use configured atomic
  durations unless explicitly used as a handoff source. Structured parallel
  branches must synchronize before terminating, including cross-model paths.
- Path counts cover every case of the first seeded replication. Replay captures
  the first 50 cases, capped at 20,000 chronological events; the UI and export
  explicitly indicate truncation. Operational KPIs aggregate all replications.
  Red waiting indicators include configured delays as well as resource queues;
  inspect resource utilization alongside them when diagnosing contention.

## Verification

`go test ./...` covers the simulation engine and the existing backend suites.
Project-specific cases exercise model-local ID collisions, cross-model case
identity, costs, shared resources, calendars and skills, experiment parameters,
embedded subprocesses, gateway synchronization, boundary alternatives, and
bounded replay with complete path counts. Frontend unit tests (`npm test` in
`frontend`) cover legacy conversion and preservation of task/event constraints
when a model is edited.

The browser/API regression suite runs against the actual frontend and backend.
It creates isolated temporary projects and deletes them afterward. Export
`SUPER_USER_EMAIL` and `SUPER_USER_PASSWORD` for an account with simulator and
modeler permissions, then run (adjust the frontend port if necessary):

```sh
docker build -f frontend/Dockerfile.e2e -t bpmngo-simulation-e2e frontend
docker run --rm \
  -e BASE_URL=http://host.docker.internal:3001 \
  -e SUPER_USER_EMAIL -e SUPER_USER_PASSWORD \
  bpmngo-simulation-e2e
```

This checks upload, diagram editing, handoffs, known cycle/cost results, replay,
bottleneck marks, log download, creation/editing through the modeler, immutable
run snapshots, revision restore, package round trips, capacity sweeps,
multi-parameter campaigns, and the issue register.
