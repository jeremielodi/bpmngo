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
import { checkContainers, ResourceMonitor } from './resources';
import { checkPg, PgLevelStats, PgTarget, startPgWindow } from './pgstats';

type Mode = 'clients' | 'rate';

interface LevelResult {
  mode: Mode;
  level: number;                 // clients, or instances started per second
  durationS: number;
  started: number;               // starts sent in the measured window
  completed: number;
  throughput: number;            // instances completed per second
  latencyMs: { p50: number; p95: number; p99: number; max: number };
  errors: Record<string, number>; // gRPC code → count (starts and job completions)
  cpuSeconds: number | null;
  cpuAvgPct: number;
  cpuPeakPct: number;
  memAvgMiB: number;
  memPeakMiB: number;
  cpuMsPerInstance: number | null;
  containers: { container: string; cpuAvgPct: number; memPeakMiB: number }[];
  pg?: PgLevelStats;             // BpmnGo: PostgreSQL over the measured window
}

const now = () => Number(process.hrtime.bigint()) / 1e6;
const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));

// Wait before the next try after n refusals in a row: 100 ms doubling up to
// 5 s, with full jitter so that refused clients do not come back together.
const backoffMs = (n: number) => Math.random() * Math.min(5000, 100 * 2 ** Math.min(n, 6));

// In rate mode, starts still waiting for an answer beyond this are not sent
// (counted as CLIENT_LIMIT): the client must not become the bottleneck.
const MAX_IN_FLIGHT = 2000;

function args(argv: string[]) {
  const get = (name: string, def?: string) => {
    const i = argv.indexOf(`--${name}`);
    return i >= 0 ? argv[i + 1] : (process.env[name.toUpperCase().replace(/-/g, '_')] ?? def);
  };
  const list = (s: string) => s.split(',').map(x => x.trim()).filter(Boolean);
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const engines: Record<string, EngineConfig> = {
    camunda: { name: 'camunda', grpc: get('camunda-grpc', 'localhost:26500')!, rest: get('camunda-rest', 'http://localhost:8080')!, user: get('camunda-user'), password: get('camunda-password') },
    bpmngo: { name: 'bpmngo', grpc: get('bpmngo-grpc', 'localhost:26600')!, rest: get('bpmngo-rest', 'http://localhost:9080')!, user: get('bpmngo-user', 'admin@goflow.com'), password: get('bpmngo-password', 'admin123') },
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
    workers: Number(get('workers', '8')),
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
  await starters[0].deploy(`${tag}.bpmn`, Buffer.from(processXml(tag)));
  await sleep(1000);

  const errors: Record<string, number> = {};
  const count = (e: unknown) => {
    const code = (e as CallError)?.code ?? 'ERROR';
    errors[code] = (errors[code] ?? 0) + 1;
  };

  // Workers: long-poll for jobs, complete each batch in parallel. They run for every level.
  let stopWorkers = false;
  const workerLoops: Promise<void>[] = [];
  for (const type of [`${tag}-first`, `${tag}-second`]) {
    for (let i = 0; i < o.workers; i++) {
      const engine = workerConn[i % workerConn.length];
      workerLoops.push((async () => {
        while (!stopWorkers) {
          let jobs;
          try {
            jobs = await engine.activateJobs(type, 32, 1000);
          } catch (e) {
            count(e);
            await sleep(100);
            continue;
          }
          await Promise.all(jobs.map(j => engine.call('CompleteJob', { jobKey: j.key, variables: '{}' }).catch(count)));
        }
      })());
    }
  }

  const startInstance = (engine: Engine) => engine.call('CreateProcessInstanceWithResult', {
    request: { bpmnProcessId: tag, version: -1, variables: VARIABLES, tenantId: '' },
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
  for (const { mode, level } of o.steps) {
    process.stdout.write(`  ${String(level).padStart(4)} ${mode === 'clients' ? 'clients ' : 'inst/s  '} … `);
    const warmEnd = Date.now() + o.warmupS * 1000;
    const measureEnd = warmEnd + o.durationS * 1000;
    const done: { start: number; end: number }[] = [];
    let started = 0;
    for (const k of Object.keys(errors)) delete errors[k];

    let monitor: ResourceMonitor | undefined;
    let pgWindow: (() => Promise<PgLevelStats>) | undefined;
    const startMonitor = (async () => {
      await sleep(Math.max(0, warmEnd - Date.now()));
      for (const k of Object.keys(errors)) delete errors[k];   // count only the measured window
      if (pg) pgWindow = await startPgWindow(pg).catch(() => undefined);
      if (containers.length) {
        monitor = new ResourceMonitor(cfg.name, containers);
        await monitor.start();
      }
    })();
    const pgAtEnd = () => pgWindow ? pgWindow().catch(() => undefined) : Promise.resolve(undefined);

    if (mode === 'clients') {
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
            if (startedAt >= warmEnd && Date.now() <= measureEnd) done.push({ start, end: now() });
          } catch (e) {
            if (startedAt >= warmEnd) count(e);
            await sleep(backoffMs(refusals++));
          }
        }
      })());
      await startMonitor;
      await Promise.all(clients);
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
        if (measured) started++;
        if (inFlight >= MAX_IN_FLIGHT) {
          if (measured) count({ code: 'CLIENT_LIMIT' });
          continue;
        }
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
      await Promise.all(pending);
      results.push({ ...levelResult(mode, level, o, started, done, errors, await usageAtEnd), pg: await pgStats });
      printLevel(results[results.length - 1]);
      continue;
    }
    const usage = monitor ? await monitor.stop() : undefined;
    results.push({ ...levelResult(mode, level, o, started, done, errors, usage), pg: await pgAtEnd() });
    printLevel(results[results.length - 1]);
  }

  stopWorkers = true;
  await Promise.all(workerLoops);
  all.forEach(e => e.close());
  return results;
}

function levelResult(mode: Mode, level: number, o: Options, started: number, done: { start: number; end: number }[],
                     errors: Record<string, number>, usage: Awaited<ReturnType<ResourceMonitor['stop']>> | undefined): LevelResult {
  const lat = done.map(d => d.end - d.start).sort((a, b) => a - b);
  const cpuSeconds = usage?.total.cpuSeconds ?? null;
  return {
    mode, level,
    durationS: o.durationS,
    started,
    completed: done.length,
    throughput: done.length / o.durationS,
    latencyMs: { p50: percentile(lat, 0.5), p95: percentile(lat, 0.95), p99: percentile(lat, 0.99), max: lat[lat.length - 1] ?? NaN },
    errors: { ...errors },
    cpuSeconds,
    cpuAvgPct: usage?.total.cpuAvgPct ?? NaN,
    cpuPeakPct: usage?.total.cpuPeakPct ?? NaN,
    memAvgMiB: usage?.total.memAvgMiB ?? NaN,
    memPeakMiB: usage?.total.memPeakMiB ?? NaN,
    containers: (usage?.containers ?? []).map(c => ({ container: c.container, cpuAvgPct: c.cpuAvgPct, memPeakMiB: c.memPeakMiB })),
    // The monitor's window starts a moment after the measured one: scale its CPU rate to the whole window.
    cpuMsPerInstance: usage && cpuSeconds !== null && done.length ? (usage.total.cpuAvgPct / 100) * o.durationS * 1000 / done.length : null,
  };
}

const errorCount = (r: LevelResult) => Object.values(r.errors).reduce((a, b) => a + b, 0);

function printLevel(r: LevelResult) {
  console.log(`${r.throughput.toFixed(1)} inst/s, p50 ${r.latencyMs.p50.toFixed(0)} ms, p99 ${r.latencyMs.p99.toFixed(0)} ms, ${errorCount(r)} errors`);
}

// A fixed rate is sustained when every start was accepted, (almost) every
// instance completed, and the p99 stays under the limit.
const sustained = (r: LevelResult, o: Options) =>
  r.mode === 'rate' && errorCount(r) === 0 && r.completed >= 0.98 * r.started && r.latencyMs.p99 <= o.p99LimitMs;

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
    `Workload  : start → 2 service tasks → end; ${o.workers} workers per job type; ` +
      `${o.warmupS} s warm-up then ${o.durationS} s measured per level`,
    `Runs      : ${o.repeat}${o.repeat > 1 ? ' (engine order alternating; values are mean±standard deviation)' : ''}`,
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
    const last = runs[runs.length - 1].bpmngo?.filter(r => r.pg?.top.length).pop();
    if (last?.pg) {
      lines.push(`Heaviest statements at ${last.level} ${last.mode === 'clients' ? 'clients' : 'inst/s'} (ms per instance, calls per instance):`);
      for (const q of last.pg.top.slice(0, 5)) {
        lines.push(`  ${f(q.execMs / last.completed, 3).padStart(7)} ms  ${f(q.calls / last.completed, 1).padStart(5)}×  ${q.query.slice(0, 110)}`);
      }
      lines.push('');
    }
  }
  const errorLines = each((engine, r, run) => Object.keys(r.errors).length
    ? `${where(engine, r, run)}: ${Object.entries(r.errors).map(([k, v]) => `${k} ×${v}`).join(', ')}` : undefined);
  if (errorLines.length) lines.push('Errors:', ...errorLines, '');
  if (o.repeat > 1) {
    lines.push('Each run:', ...each((engine, r, run) =>
      `${where(engine, r, run)}: ${f(r.throughput, 1)} inst/s, p50 ${f(r.latencyMs.p50)} ms, p99 ${f(r.latencyMs.p99)} ms, ${errorCount(r)} errors`), '');
  }
  lines.push('inst/s: instances completed per second (start to end, both jobs done). p50/p95/p99: end-to-end');
  lines.push('latency of CreateProcessInstanceWithResult. CPU avg in % of one core; CPU/inst: CPU time of the');
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
    }
    runs.push(byEngine);
    fs.writeFileSync(path.join(o.out, 'load.json'), JSON.stringify(runs, null, 2));
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
