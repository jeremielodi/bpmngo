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
- structured parallel and inclusive fork/join gateways, including nested
  forks and inclusive branches selected independently;
- asynchronous, resumable campaigns with progress, cancellation, and a
  configurable PostgreSQL-backed worker pool;
- automatic resource-capacity sweeps with target-based recommendations;
- mean, median, p90 and p95 cycle time, throughput, cost, resource utilization,
  queues, and 95% confidence intervals across replications;
- an evidence-based issue register, redesign heuristics, and as-is/to-be
  comparison.

The built-in **Helpdesk assignment** template contains the Level 1/Level 2
resources, costs, branch probabilities, repeated escalation loop, client
response delay, and activity durations used by the exercise. It can be used
as-is, or replaced by importing a `.bpmn` file.

## Workflow

1. Open **Simulation lab** and import a BPMN file or load the Helpdesk template.
2. Associate activity IDs with duration distributions, explicit waits, and
   resources. Exclusive gateway probabilities total `1`; inclusive gateway
   probabilities are independent; parallel gateways need none.
3. Run **Analyze** for deterministic expected values.
4. Use **Staffing** to calculate the minimum server count for a queueing target.
5. Use **Save & run async** for a persisted campaign. Follow progress, leave
   the page, resume tracking later, or cancel it from **Campaigns**.
6. Run a capacity sweep to compare a resource over a range and select the
   smallest capacity meeting the mean/p95 cycle-time targets.
7. Mark the result as the as-is baseline, duplicate it as a to-be scenario,
   change capacity or process parameters, and compare the new result.
8. Track detected and manually observed problems in **Issues**. Use **Modeler**
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
| `GET /engine-rest/simulation/scenarios/:id/runs` | List prior campaigns |
| `GET /engine-rest/simulation/runs/:runId` | Read a campaign and its result |
| `POST /engine-rest/simulation/runs/:runId/cancel` | Cancel queued/running work |
| `GET/POST /engine-rest/simulation/scenarios/:id/issues` | List or add issues |
| `PUT/DELETE /engine-rest/simulation/issues/:issueId` | Update or remove an issue |

Install migrations `020` and `021` before deploying this version to an
existing database. New databases receive the tables from the baseline schema.
`SIMULATION_WORKERS` controls concurrency (default `2`, maximum `16`).

## Modeling assumptions

- Duration units are minutes, costs are hourly, and arrival rates are per hour.
- Erlang-C models a stationary Poisson arrival process, exponential service,
  one FIFO queue, identical servers, and no abandonment.
- The simulation drains all cases admitted during the configured arrival
  horizon. Parallel/inclusive gateways must be structured: every selected
  branch must reach a matching gateway of the same type. Business calendars,
  priorities, preemption, and correlated arrivals are not inferred
  automatically. Represent known external delays explicitly.
- Recommendations are hypotheses to test in a separate to-be scenario, not
  automatic prescriptions.
