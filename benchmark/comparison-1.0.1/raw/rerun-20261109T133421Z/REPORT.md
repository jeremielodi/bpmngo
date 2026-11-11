# Benchmark rerun — 9 November 2026

## 1. Environment

Camunda 8.10.7, connectors 8.10.2, and BpmnGo commit
`22d88a729d5b71084081978b8336dcdb146d3a28` were run sequentially on the same
Apple Silicon Mac. Docker client/server: 29.7.2; Compose: v5.5.1;
Docker VM: 16 vCPUs and 8,318,709,760 bytes of memory. macOS: 26.6.2 (25G83).

Both stacks had `cpuset=0-1` and a total memory limit of 2 GiB: 1,536 MiB
application/orchestration plus 512 MiB PostgreSQL/connectors. Each measurement
includes its inspected limits, active-container list, idle Docker stats,
cgroup counters, and sampled Docker stats. Both engines received two minutes
of workload warmup. Builds completed before measurement. Fresh Compose volumes
were used for this run. BpmnGo used default direct mode, no Kafka, ports
9080/26600, and Basic credentials `admin@goflow.com` / `admin123`.

The identical Go client uses `zeebe-client-go/v8` v8.6.0 protobuf bindings.
Its source and dependency lock files are saved in [source](source/).
Binary SHA-256: `89b75d142a62e1c201bd4a959d2bc3959bcd2f97f414296875841d4b1b20210e`.

## 2. Conformance

47 scenarios were deployed and played twice on each engine. Camunda accepted
all models and recorded no scenario error in either pass. The existing
observation normalizer reports **35/47 identical in each pass**.
BpmnGo recorded one scenario error in pass 1 and two in pass 2. Successful
runner exit codes do not mean that every scenario matched.

| Scenario | Camunda | BpmnGo | Severity |
|---|---|---|---|
| exclusive-two-true-reference-order | Job at element `b` | Job at `a`, both passes | medium |
| boundary-message | `messageKey`, default tenant | Different publication schema | minor |
| boundary-signal-non-interrupting | `signalKey`, tenant | Proprietary broadcast schema | minor |
| job-fail-no-retries-incident | Full incident schema and pagination | Reduced schema and UUID keys | medium |
| event-subprocess-message | Camunda publication schema | Different publication schema | minor |
| message-correlation-key | Camunda publication schema | Different publication schema | minor |
| message-start-event | Job delivered, both passes | Job delivered in P1; no job observed in P2; different REST schema | blocking |
| event-based-gateway-message | Camunda publication schema | Different publication schema | minor |
| signal-intermediate-catch | `signalKey`, tenant | Proprietary broadcast schema | minor |
| user-task-rest-completion | HTTP 204, full task search | HTTP 200 with JSON, reduced task schema | medium |
| grpc-key-usable-in-rest | REST returns the gRPC integer key | Accepts it but returns a different UUID | medium |
| finished-instance-elements-and-variables | Full metadata and pagination | Reduced schema, UUIDs and differently typed values | medium |

Sources: `camunda/behavior*/result.json` and `bpmngo/behavior*/result.json`.
These counts compare the existing harness's observations; they do not certify
all BPMN semantics or full API compatibility.

## 3. Resources — behavior suite

CPU average uses cgroup CPU seconds divided by elapsed measurement time.
CPU peak and memory use Docker stats. 200% represents both assigned CPUs.

| Engine/pass | Duration | CPU time | CPU average | CPU peak | Memory at outset | Memory peak |
|---|---:|---:|---:|---:|---:|---:|
| Camunda P1 | 25 s | 7.49 s | 30.0% | 29.6% | 1,996.3 MiB | 2,006.9 MiB |
| BpmnGo P1 | 21 s | 19.51 s | 92.9% | 117.8% | 445.9 MiB | 462.3 MiB |
| BpmnGo/Camunda P1 | 0.84× | 2.60× | 3.10× | 3.98× | 0.22× | 0.23× |
| Camunda P2 | 25 s | 3.78 s | 15.1% | 50.6% | 1,539.2 MiB | 1,547.4 MiB |
| BpmnGo P2 | 23 s | 1.89 s | 8.2% | 12.0% | 451.9 MiB | 452.5 MiB |
| BpmnGo/Camunda P2 | 0.92× | 0.50× | 0.54× | 0.24× | 0.29× | 0.29× |

CPU time differs by over 20% between passes for both engines; CPU averages and
peaks also vary substantially. Camunda memory changes by over 20%. Background
engine work after warmup, cache effects, and BpmnGo's failed message-start
scenario make these resource passes unsuitable for a single efficiency ratio.

## 4. Latency

30 sequential samples per call and per pass, with zero measured API errors.
Cells show P1 p50/p95 then P2 p50/p95, in milliseconds.

| Call | Camunda p50/p95 | BpmnGo p50/p95 |
|---|---:|---:|
| Topology | 0.325/0.441 · 0.270/1.121 | 0.354/0.596 · 0.378/0.585 |
| DeployResource | 3.860/7.634 · 2.690/3.840 | 1.791/2.897 · 1.497/1.970 |
| CreateProcessInstance | 2.549/6.330 · 1.487/2.705 | 2.383/2.834 · 2.188/2.538 |
| CreateProcessInstanceWithResult, no task | 1.702/4.849 · 2.333/4.911 | 3.408/4.077 · 2.419/2.753 |
| Creation to job delivery | 3.079/9.388 · 2.225/5.041 | 3.825/5.055 · 3.121/3.680 |
| CompleteJob | 1.692/4.996 · 1.459/2.013 | 2.496/3.872 · 1.978/2.291 |
| End to end, one service task | 4.514/11.985 · 3.872/6.149 | 5.978/7.722 · 4.858/5.520 |

Changes above 20%, relative to P1: Camunda Topology p95; deployment p50/p95;
creation p50/p95; result-returning creation p50; delivery p50/p95;
completion p95; one-task end-to-end p95. BpmnGo: deployment p95;
result-returning creation p50/p95; delivery p95; completion p50/p95;
one-task end-to-end p95.

## 5. Load

Closed loop, start → two service tasks → end, business variables, 8 workers
sharing one job type, batches of 32, 10 s warmup followed by a nominal 60 s
measurement window. Latency is milliseconds. CPU/instance is an estimate from
sampled CPU average. BpmnGo CPU shows total (application/PostgreSQL).

| Clients | Engine/pass | inst/s | p50 | p95 | p99 | Errors | CPU average | CPU/instance | Memory peak |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 10 | Camunda P1 | 166.65 | 55.80 | 101.82 | 126.70 | 0 | 95.2% | 5.71 ms | 1,997 MiB |
| 10 | Camunda P2 | 163.99 | 55.59 | 107.49 | 151.80 | 0 | 92.2% | 5.62 ms | 2,005 MiB |
| 10 | BpmnGo P1 | 226.63 | 41.73 | 67.30 | 83.61 | 0 | 179.4% (66.7/112.7) | 7.92 ms | 494 MiB |
| 10 | BpmnGo P2 | 217.08 | 43.26 | 70.28 | 88.19 | 0 | 188.2% (66.4/121.9) | 8.67 ms | 487 MiB |
| 50 | Camunda P1 | 234.35 | 186.35 | 427.18 | 583.48 | 0 | 98.4% | 4.20 ms | 2,014 MiB |
| 50 | Camunda P2 | 237.30 | 186.50 | 431.27 | 561.28 | 0 | 98.5% | 4.15 ms | 1,995 MiB |
| 50 | BpmnGo P1 | 241.78 | 189.73 | 393.15 | 506.06 | 0 | 186.8% (67.5/119.3) | 7.73 ms | 572 MiB |
| 50 | BpmnGo P2 | 235.56 | 192.61 | 410.93 | 538.41 | 0 | 187.5% (65.5/122.0) | 7.96 ms | 519 MiB |
| 100 | Camunda P1 | 241.91 | 380.89 | 787.80 | 1,006.72 | 0 | 98.9% | 4.09 ms | 1,998 MiB |
| 100 | Camunda P2 | 247.96 | 375.24 | 753.90 | 906.86 | 0 | 96.0% | 3.87 ms | 1,857 MiB |
| 100 | BpmnGo P1 | 255.02 | 371.22 | 704.37 | 838.45 | 0 | 180.2% (66.0/114.2) | 7.07 ms | 506 MiB |
| 100 | BpmnGo P2 | 233.08 | 393.95 | 781.51 | 1,070.71 | 0 | 170.2% (61.3/108.9) | 7.30 ms | 615 MiB |

BpmnGo throughput is 32–36% higher at 10 clients. At 50 clients the difference
is +3.2% / -0.7%; at 100 clients +5.4% / -6.0%. No RESOURCE_EXHAUSTED or other
gRPC error occurred in load. BpmnGo's CPU/instance is about 1.4–1.9× Camunda's.
At 100 clients, BpmnGo P2 p99 increases 27.7% and memory peak increases 21.6%.
No throughput changes exceed 20%; Camunda's 10-client p99 increases 19.8%.

## 6. Limitations

- This is a rerun of the existing 47-scenario suite. It does not add missing
  gateway/join cases or prove every behavior demanded by a larger BPMN audit.
- The normalizer compares saved observations and removes generated keys;
  scenario errors are therefore reported separately above. It also sorts
  object arrays, so its match count does not prove delivery order.
- BpmnGo's default Dockerfile compiles an amd64 executable on this ARM host;
  that default was retained. Docker cpusets refer to VM CPUs, not guaranteed
  dedicated physical cores.
- CPU and memory peaks have coarse sampling resolution; behavior measurement
  durations also include collection overhead. The memory-at-outset column
  is sampled at suite startup, not after a separately verified idle period.
- Load throughput divides successful measured requests by elapsed time including
  the final in-flight request drain. The window is nominally 60 s, with actual
  elapsed seconds recorded in each JSON. Worker completion errors are not
  separately counted by this harness.
- Another task recreated `lodk-evaluation-ux-test`, then briefly launched
  `lodk-evaluation-completion-audit`. The guard interrupted the run. Overlapping
  Camunda behavior P2 and load-10 P2 were excluded and repeated. Interrupted
  artifacts remain under `invalidated-*` and `interrupted-*`; summaries use
  the replacement measurements. Preflight checks cannot prove that no brief
  unrelated host activity occurred between checks.
- The original unrelated test container was removed by that external task.
  It could not be restarted at cleanup. All benchmark containers were stopped;
  benchmark data volumes were retained.

## 7. Verdict

**6.5/10** for BpmnGo 1.0.1 as a replacement within this existing suite.
Behavior: 35/47 observations match; exclusive routing differs and message start fails in P2.
Performance: +32–36% throughput at 10 clients; roughly equal at 50–100, with variable tails.
Resources: substantially less memory, but about 1.4–1.9× CPU per load instance.
A transparent replacement still needs API and behavior validation for the intended models.

Code and measurements: [source](source/), [combined-summary.json](combined-summary.json),
[Camunda raw data](camunda/), [BpmnGo raw data](bpmngo/).
