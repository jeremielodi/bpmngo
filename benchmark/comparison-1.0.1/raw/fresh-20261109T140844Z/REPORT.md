# Fresh-image benchmark — 9 November 2026

## 1. Environment

All 142 pre-existing Docker containers were removed as requested. Their volumes
were preserved. This run used new Compose projects and new database volumes,
fresh image pulls, and an uncached BpmnGo build (`build --pull --no-cache`).

- Host: Apple M4 Max, 16 cores, 64 GiB RAM; macOS 26.6.2 (25G83), arm64.
- Docker client/server 29.7.2; Compose v5.5.1; VM: 16 vCPUs, 8,318,709,760 bytes RAM.
- Camunda: 8.10.7, connectors 8.10.2, official lightweight Compose configuration.
- BpmnGo: `22d88a729d5b71084081978b8336dcdb146d3a28`, default direct mode, no Kafka.
- Fresh `postgres:16` resolved to PostgreSQL **16.15**, Debian 16.15-1.pgdg13+2.
- Both stacks: `cpuset=0-1`, total memory 2 GiB. Application/orchestration 1,536 MiB;
  PostgreSQL/connectors 512 MiB. BpmnGo ports: 9080/26600.
- Same Go client and workload; BpmnGo alone adds Basic credentials
  `admin@goflow.com` / `admin123`. Client bindings: Zeebe Go v8.6.0.
- Two minutes of workload warmup per engine; builds finished before measurement.
  Only one engine ran at a time. The Docker start-event monitors recorded **zero
  other container starts** throughout both engine campaigns.

Exact image IDs, digests, and architecture are in [image-manifest.txt](image-manifest.txt).
The freshly pulled PostgreSQL digest is
`sha256:f9a7b9ebf6f56201f949db745e47bc423c0f0f8294bafe991cc10bf74b029657`.
Every measurement retains inspected limits, active-container lists, initial
Docker stats, cgroup CPU counters, and sampled Docker stats.

The client source and scripts are saved in [source](source/). Client binary
SHA-256: `89b75d142a62e1c201bd4a959d2bc3959bcd2f97f414296875841d4b1b20210e`.

## 2. Conformance

Both engines accepted all 47 models in both passes. **35/47 normalized
observations matched in each pass.** Camunda recorded no scenario error in P1,
and one in P2; BpmnGo recorded one in P1 and two in P2. Runner exit status alone
does not establish scenario success.

| Scenario | Camunda | BpmnGo | Severity |
|---|---|---|---|
| exclusive-two-true-reference-order | Job at `b` | Job at `a`, both passes | medium |
| boundary-message | Camunda publication schema | Different fields and tenant representation | minor |
| boundary-signal-non-interrupting | `signalKey`, tenant | Proprietary broadcast schema | minor |
| job-fail-no-retries-incident | Full incident schema and pagination | Reduced fields, UUID keys | medium |
| event-subprocess-message | Camunda publication schema | Different publication schema | minor |
| message-correlation-key | Camunda publication schema | Different publication schema | minor |
| message-start-event | P1 delivers one job; P2 none | Same job counts; different REST schema | minor schema difference; P2 behavior inconclusive |
| event-based-gateway-message | Camunda publication schema | Different publication schema | minor |
| signal-intermediate-catch | `signalKey`, tenant | Proprietary broadcast schema | minor |
| user-task-rest-completion | HTTP 204, full task metadata | HTTP 200 JSON, reduced search schema | medium |
| grpc-key-usable-in-rest | REST returns the gRPC integer key | Accepts the key but returns a UUID | medium |
| finished-instance-elements-and-variables | Full metadata and pagination | Reduced schema and differently typed variable values | medium |

The repeat of `message-start-event` is **not independent**: the harness reuses
the process/message names, `messageId`, and correlation key `S-1`; it leaves the
event-started instance active because no creation key is assigned to the normal
cleanup path. Both engines delivered a job in P1 and none in P2. This supports a
test-state explanation and does not establish a BpmnGo-specific regression.
P2's message-start result is therefore inconclusive as an independent test.

Sources: `camunda/behavior*/result.json`, `bpmngo/behavior*/result.json`, and
`source/cmd/bench/behavior_advanced.go` / `behavior.go`.

## 3. Resources — behavior suite

CPU average uses cgroup CPU seconds divided by elapsed measurement time;
peaks and memory come from Docker stats. 200% represents both assigned CPUs.

| Engine/pass | Duration | CPU time | CPU average | CPU peak | Memory at outset | Memory peak |
|---|---:|---:|---:|---:|---:|---:|
| Camunda P1 | 25 s | 6.84 s | 27.4% | 47.4% | 1,850.1 MiB | 1,861.9 MiB |
| BpmnGo P1 | 21 s | 19.69 s | 93.8% | 101.5% | 338.8 MiB | 354.7 MiB |
| BpmnGo/Camunda P1 | 0.84× | 2.88× | 3.43× | 2.14× | 0.18× | 0.19× |
| Camunda P2 | 28 s | 4.73 s | 16.9% | 23.4% | 1,829.9 MiB | 1,840.5 MiB |
| BpmnGo P2 | 24 s | 1.74 s | 7.2% | 10.3% | 355.3 MiB | 355.3 MiB |
| BpmnGo/Camunda P2 | 0.86× | 0.37× | 0.43× | 0.44× | 0.19× | 0.19× |

CPU time, CPU average, and CPU peak change by more than 20% between passes for
both engines. The suites are sensitive to remaining engine work after warmup
and test state. Their resource ratios should not be presented as a single
stable efficiency number. Memory is substantially lower for BpmnGo in both passes.

## 4. Latency

30 sequential calls per kind per pass, zero measured API errors. Values are
P1 p50/p95 · P2 p50/p95, milliseconds.

| Call | Camunda p50/p95 | BpmnGo p50/p95 |
|---|---:|---:|
| Topology | 0.293/0.607 · 0.307/0.586 | 0.394/0.565 · 0.381/0.502 |
| DeployResource | 5.327/11.831 · 3.279/6.924 | 1.572/1.906 · 1.518/1.751 |
| CreateProcessInstance | 2.073/3.873 · 1.923/5.361 | 2.279/2.725 · 2.504/2.825 |
| CreateProcessInstanceWithResult, no task | 3.939/6.188 · 1.617/5.039 | 2.360/2.564 · 2.367/2.640 |
| Creation to job delivery | 4.469/10.328 · 3.478/11.202 | 3.157/3.798 · 3.324/9.354 |
| CompleteJob | 1.887/3.025 · 1.671/8.410 | 2.019/2.232 · 2.163/3.332 |
| End to end, one service task | 5.596/9.317 · 5.720/11.031 | 5.333/5.947 · 5.053/5.765 |

Changes above 20%, relative to P1: Camunda deployment p50/p95 (-38.4%/-41.5%),
creation p95 (+38.4%), result-returning creation p50 (-58.9%), delivery p50
(-22.2%), and completion p95 (+178.0%). BpmnGo delivery p95 rises 146.3% and
completion p95 rises 49.3%.

## 5. Load

Start → two service tasks → end, business variables, closed-loop clients,
8 workers sharing the job type, batches of 32, 10 s warmup then nominal 60 s
measurement. Latencies are milliseconds; memory is MiB. CPU/instance is estimated
from sampled CPU average. BpmnGo CPU shows total (application/PostgreSQL).

| Clients | Engine/pass | inst/s | p50 | p95 | p99 | Errors | CPU average | CPU/instance | Memory peak |
|---:|---|---:|---:|---:|---:|---|---:|---:|---:|
| 10 | Camunda P1 | 181.60 | 51.18 | 95.99 | 115.60 | 0 | 96.8% | 5.33 ms | 1,930 |
| 10 | Camunda P2 | 178.41 | 51.97 | 97.60 | 124.13 | 0 | 95.9% | 5.38 ms | 1,903 |
| 10 | BpmnGo P1 | 229.25 | 41.34 | 65.18 | 78.84 | 0 | 184.1% (65.5/118.7) | 8.03 ms | 399 |
| 10 | BpmnGo P2 | 209.47 | 44.87 | 73.66 | 95.80 | 0 | 186.3% (66.0/120.4) | 8.90 ms | 487 |
| 50 | Camunda P1 | 245.18 | 175.59 | 407.31 | 535.10 | 0 | 91.2% | 3.72 ms | 1,909 |
| 50 | Camunda P2 | 222.31 | 190.46 | 466.60 | 701.24 | 0 | 91.5% | 4.12 ms | 1,875 |
| 50 | BpmnGo P1 | 248.98 | 182.97 | 383.87 | 487.61 | 0 | 189.3% (66.6/122.6) | 7.60 ms | 504 |
| 50 | BpmnGo P2 | 240.50 | 185.14 | 399.62 | 558.14 | 0 | 182.9% (61.5/121.4) | 7.60 ms | 486 |
| 100 | Camunda P1 | 235.74 | 387.22 | 834.72 | 1,143.65 | 752 RESOURCE_EXHAUSTED | 89.7% | 3.80 ms | 1,886 |
| 100 | Camunda P2 | 219.47 | 399.43 | 872.81 | 1,590.28 | 501 RESOURCE_EXHAUSTED | 88.6% | 4.04 ms | 1,905 |
| 100 | BpmnGo P1 | 250.57 | 371.29 | 721.90 | 926.78 | 0 | 186.8% (65.2/121.6) | 7.46 ms | 477 |
| 100 | BpmnGo P2 | 230.66 | 382.09 | 835.70 | 1,513.25 | 0 | 178.8% (61.4/117.4) | 7.75 ms | 635 |

Relative throughput BpmnGo/Camunda: +26.2%/+17.4% at 10 clients,
+1.5%/+8.2% at 50, and +6.3%/+5.1% at 100. Camunda backpressure is reported
separately from completed instances. BpmnGo's sampled CPU/instance is about
1.5–2.0× Camunda's; PostgreSQL accounts for about 64–66% of BpmnGo's CPU.

Pass changes exceeding 20%: p99 Camunda at 50 clients (+31.0%) and 100 (+39.1%);
p99 BpmnGo at 10 (+21.5%) and 100 (+63.3%). BpmnGo memory peaks rise 22.2% at
10 clients and 32.9% at 100. No throughput change exceeds 20%.

## 6. Limitations

- This reruns the existing 47-scenario harness, not the later 88-scenario suite.
  It does not prove complete BPMN compatibility. The original broader protocol's
  missing cases were not added by this rerun.
- Repeated event-start scenarios are not independent, as explained above.
  The current observation normalizer also removes generated keys and sorts
  object arrays, so the normalized match count does not certify delivery order.
- Docker cpusets assign VM CPUs, not guaranteed exclusive host physical cores.
  Docker start-event logs establish container isolation, not total host idleness.
- BpmnGo's default Dockerfile forces an amd64 executable inside an ARM image on
  this ARM machine. This default was retained and may affect CPU performance.
- Resource sampling is coarse. Behavior elapsed times include capture overhead;
  memory at outset is a startup sample, not a separately verified idle baseline.
  The two resource passes show large CPU differences and should remain separate.
- Load time includes the final in-flight request drain; actual elapsed seconds
  are in each result JSON. Worker completion errors are not separately counted.
- PostgreSQL was refreshed to 16.15 for this run, so differences from previous
  runs cannot be attributed solely to removing containers.
- Old data volumes and benchmark data volumes were preserved. All containers,
  including the new benchmark containers, were removed after the run.

## 7. Verdict

**7/10, provisional within this existing suite.**
Behavior: 35/47 observations match; exclusive routing and REST contracts differ; repeated message start is inconclusive.
Performance: BpmnGo is +17–26% at 10 clients and +2–8% at higher tested levels, without observed load errors.
Resources: BpmnGo uses about 19% of Camunda's behavior-suite memory, but 1.5–2.0× CPU per load instance.
The harness limitations prevent treating this score as a complete replacement qualification.

Artifacts: [combined-summary.json](combined-summary.json), [source](source/),
[Camunda raw results](camunda/), [BpmnGo raw results](bpmngo/),
and [image identities](image-manifest.txt).
Host paths in captured configuration are normalized; unrelated container inventories are excluded.
