/**
 * load.ts — load test: the same workload on Camunda 8 and on BpmnGo.
 *
 *   npm run load -- [options]
 *
 * One process (start → two service tasks → end). Two kinds of levels:
 *  - clients (--levels): that many clients each start an instance with
 *    CreateProcessInstanceWithResult, wait for its end, and start the next.
 *    A client that is refused waits before trying again (exponential backoff
 *    with jitter, as the official SDKs do).
 *  - rate (--rates): instances are started at a fixed rate, whatever the
 *    engine answers; a refused start is not retried. This gives the highest
 *    rate each engine sustains without refusals and with an acceptable p99.
 * The same pool of workers completes the jobs. After a warm-up, the bench
 * measures throughput, end-to-end latency, errors, and the CPU and memory
 * of each engine's containers. With --repeat, the whole test runs several
 * times, the engine order alternating, and the report gives mean and spread.
 * For BpmnGo, PostgreSQL's own statistics over each measured window say
 * where its CPU goes (pgstats.ts); a long --duration makes an endurance run.
 * Both engines get the same workload, from the same client code. See
 * README.md, "Load test".
 */
import * as fs from 'fs';
import * as path from 'path';
import { CallError, Engine, EngineConfig } from './engine';
import { checkContainers, cpuSecondsNow, ResourceMonitor } from './resources';
import { checkPg, PgLevelStats, PgTarget, startPgWindow } from './pgstats';

type Mode = 'clients' | 'rate';

interface LevelResult {
  mode: Mode;
  level: number;                 // clients, or instances started per second
  durationS: number;
  started: number;               // starts sent in the measured window
  completed: number;             // of those, finished before the window closed
  lateCompletions: number;       // of those, finished after it (backlog, not throughput)
  throughput: number;            // completed per second of measured window
  latencyMs: { p50: number; p95: number; p99: number; max: number };
  errors: Record<string, number>; // gRPC code → count (starts and job completions)
  errorMessages: Record<string, string[]>; // first messages of each code other than back-pressure
  cpuSeconds: number | null;
  cpuAvgPct: number;
  cpuPeakPct: number;
  memAvgMiB: number;
  memPeakMiB: number;
  cpuMsPerInstance: number | null;
  containers: { container: string; cpuAvgPct: number; memPeakMiB: number }[];
  pg?: PgLevelStats;             // BpmnGo: PostgreSQL over the measured window
  drain?: Drain;                 // after the window, until the engine is idle again
}

// Work an engine defers past the window (BpmnGo's archiver, Camunda's
// exporters) is paid after it: in the next level's window if nothing waits
// for it. After each level the workers keep running, no instance is started,
// and the bench waits until the engine's containers are back to their idle
// CPU; what that costs belongs to the level that caused it.
interface Drain {
  seconds: number;
  cpuSeconds: number | null;     // all CPU used while draining
  excessCpuSeconds: number | null; // above the idle CPU measured before level 1
  timedOut: boolean;             // not idle within --drain-timeout
  pg?: PgLevelStats;
}

// Messages are kept for codes that are not plain back-pressure, so an error
// such as INTERNAL can be traced afterwards.
const SAMPLED_MESSAGES = 5;
const isBackPressure = (code: string) => code.endsWith('RESOURCE_EXHAUSTED') || code === 'CLIENT_LIMIT';

const now = () => Number(process.hrtime.bigint()) / 1e6;
const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));

// Wait before the next try after n refusals in a row: 100 ms doubling up to
// 5 s, with full jitter so that refused clients do not come back together.
const backoffMs = (n: number) => Math.random() * Math.min(5000, 100 * 2 ** Math.min(n, 6));

// In rate mode, starts still waiting for an answer beyond this are not sent
// (counted as CLIENT_LIMIT): the client must not become the bottleneck.
const MAX_IN_FLIGHT = 2000;

// Idle CPU is measured over IDLE_SAMPLE_MS before level 1; a drain checks the
// CPU every DRAIN_CHECK_MS.
const IDLE_SAMPLE_MS = 10000;
const DRAIN_CHECK_MS = 5000;

function args(argv: string[]) {
  const get = (name: string, def?: string) => {
    const i = argv.indexOf(`--${name}`);
    return i >= 0 ? argv[i + 1] : (process.env[name.toUpperCase().replace(/-/g, '_')] ?? def);
  };
  const list = (s: string) => s.split(',').map(x => x.trim()).filter(Boolean);
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const engines: Record<string, EngineConfig> = {
    camunda: { name: 'camunda', grpc: get('camunda-grpc', 'localhost:26500')!, rest: get('camunda-rest', 'http://localhost:8080')!, user: get('camunda-user'), password: get('camunda-password') },
    bpmngo: { name: 'bpmngo', grpc: get('bpmngo-grpc', 'localhost:26600')!, rest: get('bpmngo-rest', 'http://localhost:9080')!, user: get('bpmngo-user', 'admin@bpmngo.com'), password: get('bpmngo-password', 'admin123') },
  };
  const rates = get('rates');
  const levels = get('levels', rates ? '' : '10,50,100')!;
  return {
    engines,
    run: list(get('engines', 'camunda,bpmngo')!),
    steps: [
      ...list(levels).map(Number).map(level => ({ mode: 'clients' as Mode, level })),
      ...list(rates ?? '').map(Number).map(level => ({ mode: 'rate' as Mode, level })),
    ],
    durationS: Number(get('duration', '60')),
    warmupS: Number(get('warmup', '10')),
    // The first level's warm-up also has to absorb the engine's cold start
    // (JVM class loading and JIT, connection pools, buffer cache). 10 s is
    // not enough for Camunda: its per-instance CPU cost then falls by ~5x
    // between level 1 and level 2, which is a measurement artifact, not a
    // property of the engine. --first-warmup replaces --warmup on level 1.
    firstWarmupS: Number(get('first-warmup', '60')),
    // Longest wait for the engine to drain after a level; 0 skips draining.
    drainTimeoutS: Number(get('drain-timeout', '300')),
    workers: Number(get('workers', '8')),
    // A real model from bpmn/ instead of the generated 4-element process:
    // realistic models cost parser time at deploy and more jobs per instance,
    // neither of which the generated one can show. Job types are read from the
    // model's zeebe:taskDefinition elements.
    processFile: get('process'),
    repeat: Math.max(1, Number(get('repeat', '1'))),
    p99LimitMs: Number(get('p99-limit', '1000')),
    containers: {
      camunda: list(get('camunda-containers', 'orchestration')!),
      bpmngo: list(get('bpmngo-containers', 'bpmngo-app,bpmngo-postgres,bpmngo-redpanda')!),
    } as Record<string, string[]>,
    resources: !argv.includes('--no-resources'),
    // BpmnGo's PostgreSQL container: by default the measured one named *postgres*.
    pg: (() => {
      const container = get('bpmngo-postgres') ?? list(get('bpmngo-containers', 'bpmngo-app,bpmngo-postgres,bpmngo-redpanda')!).find(c => c.includes('postgres'));
      return argv.includes('--no-pg-stats') || !container ? undefined
        : { container, user: get('pg-user', 'goflow')!, db: get('pg-db', 'goflow')! } as PgTarget;
    })(),
    out: get('out', path.join('results', `load-${stamp}`))!,
  };
}
type Options = ReturnType<typeof args>;

const processXml = (id: string) => `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="${id}_defs" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="${id}" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:sequenceFlow id="f0" sourceRef="start" targetRef="first"/>
    <bpmn:serviceTask id="first"><bpmn:extensionElements><zeebe:taskDefinition type="${id}-first"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:sequenceFlow id="f1" sourceRef="first" targetRef="second"/>
    <bpmn:serviceTask id="second"><bpmn:extensionElements><zeebe:taskDefinition type="${id}-second"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:sequenceFlow id="f2" sourceRef="second" targetRef="end"/>
    <bpmn:endEvent id="end"/>
  </bpmn:process>
</bpmn:definitions>`;

// A few typical business variables, the same for every instance.
const VARIABLES = JSON.stringify({ orderId: 'O-1', amount: 129.9, currency: 'EUR', customer: { id: 'C-42', vip: false }, items: [{ sku: 'A', qty: 2 }, { sku: 'B', qty: 1 }] });

function percentile(sorted: number[], p: number) {
  return sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(p * sorted.length))] : NaN;
}

async function runEngine(cfg: EngineConfig, o: Options): Promise<LevelResult[]> {
  // Separate connections for starters and workers, so neither waits on the other's streams.
  const starters = [0, 1, 2, 3].map(() => new Engine(cfg));
  const workerConn = [0, 1].map(() => new Engine(cfg));
  const all = [...starters, ...workerConn];
  const tag = `load_${Date.now().toString(36)}`;
  let processId = tag;
  let types = [`${tag}-first`, `${tag}-second`];
  if (o.processFile) {
    const xml = fs.readFileSync(path.resolve(o.processFile), 'utf8');
    const id = /<bpmn:process[^>]*\bid="([^"]+)"/.exec(xml)?.[1];
    if (!id) throw new Error(`--process: no <bpmn:process id="…"> in ${o.processFile}`);
    const found = [...xml.matchAll(/<zeebe:taskDefinition\s+type="([^"]+)"/g)].map(m => m[1]);
    if (!found.length) throw new Error(`--process: no zeebe:taskDefinition type in ${o.processFile}`);
    processId = id;
    types = [...new Set(found)];
    await starters[0].deploy(path.basename(o.processFile), Buffer.from(xml));
  } else {
    await starters[0].deploy(`${tag}.bpmn`, Buffer.from(processXml(tag)));
  }
  await sleep(1000);
  console.log(`  process ${processId}, ${types.length} job types: ${types.join(', ')}`);

  // Worker errors are kept apart (`job <code>`): they are shown, but only a
  // refused start makes a rate level not sustained.
  const errors: Record<string, number> = {};
  const errorMessages: Record<string, string[]> = {};
  const count = (e: unknown, prefix = '') => {
    const code = prefix + ((e as CallError)?.code ?? 'ERROR');
    errors[code] = (errors[code] ?? 0) + 1;
    const message = (e as CallError)?.message;
    if (message && !isBackPressure(code) && (errorMessages[code] ??= []).length < SAMPLED_MESSAGES) errorMessages[code].push(message);
  };
  const resetErrors = () => {
    for (const k of Object.keys(errors)) delete errors[k];
    for (const k of Object.keys(errorMessages)) delete errorMessages[k];
  };
  const countJob = (e: unknown) => count(e, JOB_ERROR);

  // A refused completion is retried after the same backoff as a refused start,
  // as the official SDKs do. Dropping it would leave the job locked for its
  // whole activation timeout (60 s) and punish the engine that pushes back on
  // job commands (Camunda) for a behaviour its real clients do not have.
  const completeJob = async (engine: Engine, key: string) => {
    for (let refusals = 0; ; refusals++) {
      try {
        await engine.call('CompleteJob', { jobKey: key, variables: '{}' });
        return;
      } catch (e) {
        countJob(e);
        if ((e as CallError)?.code !== 'RESOURCE_EXHAUSTED' || stopWorkers) return;
        await sleep(backoffMs(refusals));
      }
    }
  };

  // Workers: long-poll for jobs, complete each batch in parallel. They run for every level.
  let stopWorkers = false;
  const workerLoops: Promise<void>[] = [];
  for (const type of types) {
    for (let i = 0; i < o.workers; i++) {
      const engine = workerConn[i % workerConn.length];
      workerLoops.push((async () => {
        while (!stopWorkers) {
          let jobs;
          try {
            jobs = await engine.activateJobs(type, 32, 1000);
          } catch (e) {
            countJob(e);
            await sleep(100);
            continue;
          }
          await Promise.all(jobs.map(j => completeJob(engine, j.key)));
        }
      })());
    }
  }

  const startInstance = (engine: Engine) => engine.call('CreateProcessInstanceWithResult', {
    request: { bpmnProcessId: processId, version: -1, variables: VARIABLES, tenantId: '' },
    requestTimeout: 30000, fetchVariables: [],
  }, 40000);

  const results: LevelResult[] = [];
  const containers = o.resources ? await checkContainers(cfg.name, o.containers[cfg.name] ?? []) : [];
  let pg = cfg.name === 'bpmngo' ? o.pg : undefined;
  if (pg) {
    const problem = await checkPg(pg);
    if (problem) console.log(`  ⚠ ${problem}`);
    if (problem?.startsWith('cannot')) pg = undefined;
  }
  // Idle CPU of the engine's containers, workers polling, nothing started:
  // the level a drain waits to get back to.
  let idleCpuPct: number | null = null;
  if (o.drainTimeoutS > 0 && containers.length) {
    const c0 = await cpuSecondsNow(containers);
    await sleep(IDLE_SAMPLE_MS);
    const c1 = await cpuSecondsNow(containers);
    if (c0 !== null && c1 !== null) idleCpuPct = (c1 - c0) / (IDLE_SAMPLE_MS / 1000) * 100;
  }
  const drain = async (): Promise<Drain | undefined> => {
    if (idleCpuPct === null) return undefined;
    // Idle again: two checks in a row at most 50 % (and at least 10 points)
    // above the idle CPU. Camunda's JVM idles noisily, hence the margin.
    const limit = idleCpuPct + Math.max(10, idleCpuPct / 2);
    const t0 = Date.now();
    const c0 = await cpuSecondsNow(containers);
    const pgDrain = pg ? await startPgWindow(pg).catch(() => undefined) : undefined;
    let quiet = 0, prev = c0, prevT = t0;
    while (quiet < 2 && prev !== null && Date.now() - t0 < o.drainTimeoutS * 1000) {
      await sleep(DRAIN_CHECK_MS);
      const c = await cpuSecondsNow(containers);
      const t = Date.now();
      if (c === null) { prev = null; break; }
      quiet = (c - prev) / ((t - prevT) / 1000) * 100 <= limit ? quiet + 1 : 0;
      prev = c; prevT = t;
    }
    const seconds = (prevT - t0) / 1000;
    const cpuSeconds = c0 !== null && prev !== null ? prev - c0 : null;
    return {
      seconds, cpuSeconds,
      excessCpuSeconds: cpuSeconds === null ? null : Math.max(0, cpuSeconds - idleCpuPct / 100 * seconds),
      timedOut: quiet < 2,
      pg: pgDrain ? await pgDrain().catch(() => undefined) : undefined,
    };
  };

  for (const [stepIndex, { mode, level }] of o.steps.entries()) {
    process.stdout.write(`  ${String(level).padStart(4)} ${mode === 'clients' ? 'clients ' : 'inst/s  '} … `);
    const warmS = stepIndex === 0 ? Math.max(o.warmupS, o.firstWarmupS) : o.warmupS;
    const warmEnd = Date.now() + warmS * 1000;
    const measureEnd = warmEnd + o.durationS * 1000;
    const done: { start: number; end: number }[] = [];
    let started = 0;
    resetErrors();

    let monitor: ResourceMonitor | undefined;
    let pgWindow: (() => Promise<PgLevelStats>) | undefined;
    const startMonitor = (async () => {
      await sleep(Math.max(0, warmEnd - Date.now()));
      resetErrors();   // count only the measured window
      if (pg) pgWindow = await startPgWindow(pg).catch(() => undefined);
      if (containers.length) {
        monitor = new ResourceMonitor(cfg.name, containers);
        await monitor.start();
      }
    })();
    const pgAtEnd = () => pgWindow ? pgWindow().catch(() => undefined) : Promise.resolve(undefined);

    if (mode === 'clients') {
      // As in rate mode, a start that finishes after the window is a late
      // completion: counted apart, not as throughput, but no longer dropped.
      let inWindow = 0;
      const clients = Array.from({ length: level }, (_, i) => (async () => {
        const engine = starters[i % starters.length];
        let refusals = 0;
        while (Date.now() < measureEnd) {
          const start = now();
          const startedAt = Date.now();
          if (startedAt >= warmEnd) started++;
          try {
            await startInstance(engine);
            refusals = 0;
            if (startedAt >= warmEnd) {
              done.push({ start, end: now() });
              if (Date.now() <= measureEnd) inWindow++;
            }
          } catch (e) {
            if (startedAt >= warmEnd) count(e);
            await sleep(backoffMs(refusals++));
          }
        }
      })());
      await startMonitor;
      await Promise.all(clients);
      const usage = monitor ? await monitor.stop() : undefined;
      results.push({ ...levelResult(mode, level, o, started, done, inWindow, errors, errorMessages, usage), pg: await pgAtEnd() });
    } else {
      // Open loop: one start every 1000/level ms. Every instance started in
      // the measured window counts once it completes, even after the window.
      const interval = 1000 / level;
      const pending: Promise<void>[] = [];
      let inFlight = 0, n = 0, next = Date.now();
      while (Date.now() < measureEnd) {
        const wait = next - Date.now();
        if (wait > 0) await sleep(wait);
        next += interval;
        const measured = Date.now() >= warmEnd;
        if (inFlight >= MAX_IN_FLIGHT) {
          if (measured) count({ code: 'CLIENT_LIMIT' });
          continue;
        }
        if (measured) started++;   // a start that is never sent is not a start
        inFlight++;
        const start = now();
        pending.push(startInstance(starters[n++ % starters.length]).then(
          () => { if (measured) done.push({ start, end: now() }); },
          e => { if (measured) count(e); },
        ).finally(() => { inFlight--; }));
      }
      await startMonitor;
      const usageAtEnd = monitor ? monitor.stop() : undefined;   // resources over the window only
      const pgStats = pgAtEnd();
      monitor = undefined;
      // Completions that land after the window closes are not throughput: they
      // are the backlog the engine was still working through. Count them apart,
      // so an engine that queues instead of refusing is not credited with work
      // that happened after the measurement stopped.
      const completedInWindow = done.length;
      await Promise.all(pending);
      results.push({ ...levelResult(mode, level, o, started, done, completedInWindow, errors, errorMessages, await usageAtEnd), pg: await pgStats });
    }
    const r = results[results.length - 1];
    r.drain = await drain();
    printLevel(r);
  }

  stopWorkers = true;
  await Promise.all(workerLoops);
  all.forEach(e => e.close());
  return results;
}

function levelResult(mode: Mode, level: number, o: Options, started: number, done: { start: number; end: number }[],
                     completedInWindow: number, errors: Record<string, number>, errorMessages: Record<string, string[]>,
                     usage: Awaited<ReturnType<ResourceMonitor['stop']>> | undefined): LevelResult {
  const lat = done.map(d => d.end - d.start).sort((a, b) => a - b);
  const cpuSeconds = usage?.total.cpuSeconds ?? null;
  return {
    mode, level,
    durationS: o.durationS,
    started,
    completed: completedInWindow,
    lateCompletions: done.length - completedInWindow,
    throughput: completedInWindow / o.durationS,
    latencyMs: { p50: percentile(lat, 0.5), p95: percentile(lat, 0.95), p99: percentile(lat, 0.99), max: lat[lat.length - 1] ?? NaN },
    errors: { ...errors },
    errorMessages: Object.fromEntries(Object.entries(errorMessages).map(([k, v]) => [k, [...v]])),
    cpuSeconds,
    cpuAvgPct: usage?.total.cpuAvgPct ?? NaN,
    cpuPeakPct: usage?.total.cpuPeakPct ?? NaN,
    memAvgMiB: usage?.total.memAvgMiB ?? NaN,
    memPeakMiB: usage?.total.memPeakMiB ?? NaN,
    containers: (usage?.containers ?? []).map(c => ({ container: c.container, cpuAvgPct: c.cpuAvgPct, memPeakMiB: c.memPeakMiB })),
    // The monitor's window starts a moment after the measured one: scale its CPU rate to the whole window.
    cpuMsPerInstance: usage && cpuSeconds !== null && completedInWindow ? (usage.total.cpuAvgPct / 100) * o.durationS * 1000 / completedInWindow : null,
  };
}

const JOB_ERROR = 'job ';
const errorCount = (r: LevelResult) => Object.values(r.errors).reduce((a, b) => a + b, 0);
const startErrorCount = (r: LevelResult) => Object.entries(r.errors)
  .filter(([k]) => !k.startsWith(JOB_ERROR)).reduce((a, [, v]) => a + v, 0);

function printLevel(r: LevelResult) {
  console.log(`${r.throughput.toFixed(1)} inst/s, p50 ${r.latencyMs.p50.toFixed(0)} ms, p99 ${r.latencyMs.p99.toFixed(0)} ms, ${errorCount(r)} errors`);
}

// A fixed rate is sustained when every start was accepted, (almost) every
// instance completed, and the p99 stays under the limit.
const sustained = (r: LevelResult, o: Options) =>
  r.mode === 'rate' && startErrorCount(r) === 0 && r.completed >= 0.98 * r.started && r.latencyMs.p99 <= o.p99LimitMs;

function stats(values: number[]) {
  const v = values.filter(x => !Number.isNaN(x));
  if (!v.length) return { mean: NaN, sd: NaN };
  const mean = v.reduce((a, b) => a + b, 0) / v.length;
  const sd = v.length > 1 ? Math.sqrt(v.reduce((a, b) => a + (b - mean) ** 2, 0) / (v.length - 1)) : 0;
  return { mean, sd };
}

function report(runs: Record<string, LevelResult[]>[], o: Options): string {
  const f = (v: number | null | undefined, d = 0) => (v === null || v === undefined || Number.isNaN(v) ? '—' : v.toFixed(d));
  const ms = (s: { mean: number; sd: number }) => o.repeat > 1 ? `${f(s.mean)}±${f(s.sd)}` : f(s.mean);
  const engines = [...new Set(runs.flatMap(r => Object.keys(r)))];
  const lines = [
    'BpmnGo ⇄ Camunda 8 load test',
    `Date      : ${new Date().toISOString()}`,
    `Workload  : ${o.processFile ? `${o.processFile} (deployed as it is)` : 'start → 2 service tasks → end'}; ` +
      `${o.workers} workers per job type; ` +
      `${o.warmupS} s warm-up (${Math.max(o.warmupS, o.firstWarmupS)} s on the first level) then ${o.durationS} s measured per level`,
    `Runs      : ${o.repeat}${o.repeat > 1 ? ' (engine order alternating; values are mean±standard deviation)'
      : ' — single pass, no variance. Use --repeat 3 for figures you publish.'}`,
    '',
    `  ${'engine'.padEnd(9)}${'level'.padStart(12)}${'inst/s'.padStart(12)}${'p50'.padStart(12)}${'p95'.padStart(12)}${'p99'.padStart(14)}` +
      `${'errors'.padStart(9)}${'CPU avg'.padStart(9)}${'CPU/inst'.padStart(10)}${'mem peak'.padStart(10)}`,
  ];
  for (const { mode, level } of o.steps) {
    for (const engine of engines) {
      const rs = runs.map(r => r[engine]?.find(x => x.mode === mode && x.level === level)).filter((x): x is LevelResult => !!x);
      if (!rs.length) continue;
      const pick = (g: (r: LevelResult) => number) => stats(rs.map(g));
      const tp = pick(r => r.throughput);
      const label = mode === 'clients' ? `${level} clients` : `${level} inst/s`;
      const ok = mode === 'rate' ? ` ${rs.filter(r => sustained(r, o)).length}/${rs.length} sustained` : '';
      lines.push(`  ${engine.padEnd(9)}${label.padStart(12)}${(o.repeat > 1 ? `${f(tp.mean, 1)}±${f(tp.sd, 1)}` : f(tp.mean, 1)).padStart(12)}` +
        `${(ms(pick(r => r.latencyMs.p50)) + ' ms').padStart(12)}${(ms(pick(r => r.latencyMs.p95)) + ' ms').padStart(12)}` +
        `${(ms(pick(r => r.latencyMs.p99)) + ' ms').padStart(14)}${f(pick(errorCount).mean).padStart(9)}` +
        `${(f(pick(r => r.cpuAvgPct).mean) + ' %').padStart(9)}${(f(pick(r => r.cpuMsPerInstance ?? NaN).mean, 1) + ' ms').padStart(10)}` +
        `${(f(pick(r => r.memPeakMiB).mean) + ' MiB').padStart(10)}${ok}`);
    }
    lines.push('');
  }
  const rateSteps = o.steps.filter(s => s.mode === 'rate');
  if (rateSteps.length) {
    lines.push(`Highest fixed rate sustained (no refusal, ≥98 % completed, p99 ≤ ${o.p99LimitMs} ms), per run:`);
    for (const engine of engines) {
      const best = runs.map(r => Math.max(0, ...(r[engine] ?? []).filter(x => sustained(x, o)).map(x => x.level)));
      lines.push(`  ${engine.padEnd(9)} ${best.map(b => b ? `${b} inst/s` : `below ${rateSteps[0].level} inst/s`).join(', ')}`);
    }
    lines.push('');
    // Why each rate was not sustained: refused (the engine pushes back) or
    // queued (it accepts everything and the p99 blows past the limit). Two
    // different failure modes that one "sustained" column hides.
    const verdict = (r: LevelResult) => {
      if (sustained(r, o)) return 'sustained';
      const why: string[] = [];
      if (r.started - r.completed > 0) why.push(`${r.started - r.completed} of ${r.started} starts never completed`);
      if (r.latencyMs.p99 > o.p99LimitMs) why.push(`p99 ${f(r.latencyMs.p99)} ms > ${o.p99LimitMs} ms`);
      return `NOT sustained — ${why.join(', ') || 'errors'}`;
    };
    lines.push('Per level:');
    for (const engine of engines) {
      const rs = runs.flatMap(r => r[engine] ?? []).filter(x => x.mode === 'rate');
      if (!rs.length) continue;
      const byLevel = new Map<number, string[]>();
      runs.forEach(r => (r[engine] ?? []).filter(x => x.mode === 'rate')
        .forEach(x => byLevel.set(x.level, [...(byLevel.get(x.level) ?? []), verdict(x)])));
      for (const level of [...byLevel.keys()].sort((a, b) => a - b)) {
        const seen = [...new Set(byLevel.get(level)!)].join(' · ');
        lines.push(`  ${engine} at ${level} inst/s: ${seen}`);
      }
    }
    lines.push('');
  }
  const each = (g: (engine: string, r: LevelResult, run: number) => string | undefined) =>
    runs.flatMap((byEngine, run) => Object.entries(byEngine).flatMap(([engine, rs]) => rs.map(r => g(engine, r, run)))).filter((x): x is string => !!x);
  const where = (engine: string, r: LevelResult, run: number) =>
    `  ${engine} at ${r.level} ${r.mode === 'clients' ? 'clients' : 'inst/s'}${o.repeat > 1 ? ` (run ${run + 1})` : ''}`;
  const split = each((engine, r, run) => r.containers.length > 1
    ? `${where(engine, r, run)}: ${r.containers.map(c => `${c.container} ${f(c.cpuAvgPct)} % / ${f(c.memPeakMiB)} MiB`).join(', ')}` : undefined);
  if (split.length) lines.push('CPU avg / memory peak by container:', ...split, '');
  const pgLines = each((engine, r, run) => {
    if (!r.pg || !r.completed) return undefined;
    const per = (v: number) => v / r.completed;
    const st = r.pg.statements;
    return `${where(engine, r, run)}: ` + (st
      ? `${f(per(st.calls), 1)} statements, ${f(per(st.execMs), 2)} ms executing + ${f(per(st.planMs), 2)} ms planning, `
      : '') + `${f(per(r.pg.commits), 1)} commits, ${f(per(r.pg.walBytes) / 1024, 1)} KiB WAL`;
  });
  if (pgLines.length) {
    lines.push('PostgreSQL per completed instance (BpmnGo; planning counts only with pg_stat_statements.track_planning):', ...pgLines, '');
    const last = runs[runs.length - 1].bpmngo?.filter(r => r.pg?.top.length && r.completed > 0).pop();
    if (last?.pg) {
      lines.push(`Heaviest statements at ${last.level} ${last.mode === 'clients' ? 'clients' : 'inst/s'} (ms per instance, calls per instance):`);
      for (const q of last.pg.top.slice(0, 5)) {
        lines.push(`  ${f(q.execMs / last.completed, 3).padStart(7)} ms  ${f(q.calls / last.completed, 1).padStart(5)}×  ${q.query.slice(0, 110)}`);
      }
      lines.push('');
    }
  }
  const lateLines = each((engine, r, run) => r.lateCompletions > 0
    ? `${where(engine, r, run)}: ${r.lateCompletions} starts finished after the window ` +
      `(backlog, excluded from inst/s — the window counts ${r.completed})` : undefined);
  if (lateLines.length) lines.push('Backlog at the end of the window:', ...lateLines, '');
  const errorLines = each((engine, r, run) => Object.keys(r.errors).length
    ? `${where(engine, r, run)}: ${Object.entries(r.errors).map(([k, v]) => `${k} ×${v}`).join(', ')}` : undefined);
  if (errorLines.length) lines.push('Errors:', ...errorLines, '');
  const messageLines = each((engine, r, run) => Object.keys(r.errorMessages ?? {}).length
    ? Object.entries(r.errorMessages).map(([k, v]) => `${where(engine, r, run)}: ${k}: ${[...new Set(v)].join(' | ')}`).join('\n') : undefined);
  if (messageLines.length) lines.push('Error messages (first ones of each code other than back-pressure):', ...messageLines, '');

  // Durable cost: the window's CPU plus what the engine spent above idle
  // finishing the level's deferred work, per instance that finished at all.
  const drained = runs.some(r => Object.values(r).some(rs => rs.some(x => x.drain)));
  if (drained) {
    lines.push(`After each level, until the engine's CPU is back to idle (at most ${o.drainTimeoutS} s):`);
    lines.push(`  ${'engine'.padEnd(9)}${'level'.padStart(12)}${'drain'.padStart(10)}${'CPU above idle'.padStart(16)}` +
      `${'CPU/inst window'.padStart(17)}${'CPU/inst durable'.padStart(18)}${'SQL/inst durable'.padStart(18)}${'timeouts'.padStart(10)}`);
    for (const { mode, level } of o.steps) {
      for (const engine of engines) {
        const rs = runs.map(r => r[engine]?.find(x => x.mode === mode && x.level === level)).filter((x): x is LevelResult => !!x?.drain);
        if (!rs.length) continue;
        const finished = (r: LevelResult) => r.completed + r.lateCompletions;
        const durable = (r: LevelResult) => r.drain!.excessCpuSeconds === null || !finished(r) ? NaN
          : ((r.cpuAvgPct / 100) * r.durationS + r.drain!.excessCpuSeconds) * 1000 / finished(r);
        const sql = (r: LevelResult) => r.pg?.statements && r.drain!.pg?.statements && finished(r)
          ? (r.pg.statements.calls + r.drain!.pg.statements.calls) / finished(r) : NaN;
        const pick = (g: (r: LevelResult) => number) => stats(rs.map(g));
        const label = mode === 'clients' ? `${level} clients` : `${level} inst/s`;
        lines.push(`  ${engine.padEnd(9)}${label.padStart(12)}${(ms(pick(r => r.drain!.seconds)) + ' s').padStart(10)}` +
          `${(ms(pick(r => r.drain!.excessCpuSeconds ?? NaN)) + ' s').padStart(16)}` +
          `${(f(pick(r => r.cpuMsPerInstance ?? NaN).mean, 1) + ' ms').padStart(17)}${(f(pick(durable).mean, 1) + ' ms').padStart(18)}` +
          `${f(pick(sql).mean, 1).padStart(18)}${String(rs.filter(r => r.drain!.timedOut).length).padStart(10)}`);
      }
    }
    lines.push('  CPU/inst durable = (window CPU + CPU above idle while draining) / instances started in the window that finished;');
    lines.push('  SQL/inst durable = BpmnGo statements in the window and the drain, per such instance. A timeout means the');
    lines.push('  engine was still busy when the drain gave up: its durable figures are then a lower bound.');
    lines.push('');
  }
  if (o.repeat > 1) {
    lines.push('Each run:', ...each((engine, r, run) =>
      `${where(engine, r, run)}: ${f(r.throughput, 1)} inst/s, p50 ${f(r.latencyMs.p50)} ms, p99 ${f(r.latencyMs.p99)} ms, ${errorCount(r)} errors`), '');
  }
  lines.push('inst/s: instances *started* in the measured window that finished inside it, per second —');
  lines.push('completions after the window are backlog, reported separately. p50/p95/p99: end-to-end latency of');
  lines.push('CreateProcessInstanceWithResult. CPU avg in % of one core; CPU/inst: CPU time of the');
  lines.push("engine's containers per completed instance. Resources measured over the measured window only.");
  lines.push('Clients levels: a refused client waits 0–100 ms, doubling up to 5 s, before trying again.');
  lines.push('Rate levels: starts sent at a fixed rate, a refused start is not retried.');
  return lines.join('\n');
}

async function main() {
  const o = args(process.argv.slice(2));
  if (!o.steps.length) throw new Error('nothing to run: give --levels and/or --rates');
  fs.mkdirSync(o.out, { recursive: true });
  for (const name of o.run) {
    const cfg = o.engines[name];
    if (!cfg) throw new Error(`unknown engine "${name}" (use camunda and/or bpmngo)`);
    const probe = new Engine(cfg);
    try {
      await probe.call('Topology', {}, 10000);
      probe.close();
    } catch (e: any) {
      console.error(`✖ ${name}: no answer from the gRPC gateway at ${cfg.grpc} (${e.code}: ${e.message})`);
      process.exit(2);
    }
  }
  const runs: Record<string, LevelResult[]>[] = [];
  for (let i = 0; i < o.repeat; i++) {
    const order = i % 2 ? [...o.run].reverse() : o.run;
    const byEngine: Record<string, LevelResult[]> = {};
    for (const name of order) {
      console.log(`\n▶ ${name}${o.repeat > 1 ? ` (run ${i + 1}/${o.repeat})` : ''}`);
      byEngine[name] = await runEngine(o.engines[name], o);
      // After each engine, so a series cut short keeps what it measured.
      fs.writeFileSync(path.join(o.out, 'load.json'), JSON.stringify([...runs, byEngine], null, 2));
    }
    runs.push(byEngine);
  }
  const text = report(runs, o);
  fs.writeFileSync(path.join(o.out, 'load-report.txt'), text);
  console.log('\n' + text);
  console.log(`\nFull report: ${path.join(o.out, 'load-report.txt')}`);
}

main().catch(err => {
  console.error(err);
  process.exit(2);
});
