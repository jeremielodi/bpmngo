# BPMN simulation laboratory

The **Simulation lab** page analyzes a BPMN model without deploying it or
creating production process instances. It reuses BpmnGo's BPMN parser and
graph builder, while keeping the simulation runtime independent from the
transactional runtime.

## What it covers

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

The studio starts with a neutral scenario rather than a domain-specific
workflow. Importing a `.bpmn` file discovers tasks and configurable gateway
flows automatically, initializes safe placeholder assumptions, and preserves
matching parameters when the model is replaced by a newer revision. The
built-in **Support operations** scenario is an optional product example, not
a required workflow or a special execution mode.

## Workflow

1. Open **Simulation studio**, start a scenario, and import any BPMN 2.0 file
   (or load the optional Support operations example).
2. Review the automatically discovered activities and branches. Configure the
   demand pattern, calendars, shifts, resources, skills, routing priorities,
   duration distributions, and explicit waits.
   Exclusive gateway probabilities total `1`; inclusive gateway probabilities
   are independent; parallel gateways need none.
3. Run **Analyze** for deterministic expected values.
4. Use **Capacity planning** to calculate the minimum server count for a
   queueing target.
5. Use **Run experiment** for a persisted campaign. Follow progress, leave
   the page, resume tracking later, or cancel it from **Campaigns**.
6. Run a capacity sweep to compare a resource over a range and select the
   smallest capacity meeting the mean/p95 cycle-time targets.
7. Run a multi-parameter experiment to compare combinations and optimize mean
   cycle, p95 cycle, cost, or throughput under optional constraints.
8. Mark the result as the as-is baseline, duplicate it as a to-be scenario,
   change capacity or process parameters, and compare the new result.
9. Track detected and manually observed problems in **Issues**. Use **Modeler**
   to draw the selected to-be process.

## API

All endpoints require authentication. Analysis permissions use
`CAN_VIEW_METRICS`; mutations use the existing modeler permissions.

| Method and path | Purpose |
|---|---|
| `POST /engine-rest/simulation/analyze` | Analyze a scenario without saving it |
| `POST /engine-rest/simulation/simulate` | Simulate a scenario without saving it |
| `POST /engine-rest/simulation/staffing` | Calculate an Erlang-C staffing curve |
| `GET/POST /engine-rest/simulation/scenarios` | List or create scenarios |
| `GET/PUT/DELETE /engine-rest/simulation/scenarios/:id` | Read, update, or delete a scenario |
| `POST /engine-rest/simulation/scenarios/:id/runs` | Queue a persisted simulation |
| `POST /engine-rest/simulation/scenarios/:id/capacity-sweeps` | Queue an automatic capacity comparison |
| `POST /engine-rest/simulation/scenarios/:id/experiments` | Queue a full-factorial multi-parameter experiment |
| `GET /engine-rest/simulation/scenarios/:id/runs` | List prior campaigns |
| `GET /engine-rest/simulation/runs/:runId` | Read a campaign and its result |
| `POST /engine-rest/simulation/runs/:runId/cancel` | Cancel queued/running work |
| `GET/POST /engine-rest/simulation/scenarios/:id/issues` | List or add issues |
| `PUT/DELETE /engine-rest/simulation/issues/:issueId` | Update or remove an issue |

Install migrations `020`, `021`, and `022` before deploying this version to an
existing database. New databases receive the tables from the baseline schema.
`SIMULATION_WORKERS` controls concurrency (default `2`, maximum `16`).

## Modeling assumptions

- Duration units are minutes, costs are hourly, and arrival rates are per hour.
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
- Recommendations are hypotheses to test in a separate to-be scenario, not
  automatic prescriptions.
