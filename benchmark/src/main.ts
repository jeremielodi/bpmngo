
/**
 * main.ts — command line.
 *
 *   npm run compare -- [options]
 *
 * Runs every scenario on Camunda 8 and on BpmnGo, saves both traces, and
 * writes a report of the differences. See README.md.
 */
import * as fs from 'fs';
import * as path from 'path';
import { API_CHECKS, runApiChecks } from './apichecks';
import { compareTraces, KnownDifference, ScenarioResult, textReport } from './compare';
import { Engine, EngineConfig } from './engine';
import { checkContainers, EngineUsage, ResourceMonitor, resourcesReport } from './resources';
import { runScenario, Trace } from './runner';
import { SCENARIOS } from '../scenarios';

interface Options {
  camunda: EngineConfig;
  bpmngo: EngineConfig;
  engines: string[];
  only?: string;
  out: string;
  reference?: string;
  skipApi: boolean;
  resources: boolean;
  containers: Record<string, string[]>;
}

function parseArgs(argv: string[]): Options {
  const get = (name: string, def?: string) => {
    const i = argv.indexOf(`--${name}`);
    return i >= 0 ? argv[i + 1] : (process.env[name.toUpperCase().replace(/-/g, '_')] ?? def);
  };
  if (argv.includes('--help') || argv.includes('-h')) {
    console.log(fs.readFileSync(path.join(__dirname, '..', 'README.md'), 'utf8').split('## Options')[1]?.split('\n## ')[0] ?? '');
    process.exit(0);
  }
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  return {
    camunda: {
      name: 'camunda',
      grpc: get('camunda-grpc', 'localhost:26500')!,
      rest: get('camunda-rest', 'http://localhost:8080')!,
      user: get('camunda-user'),
      password: get('camunda-password'),
    },
    bpmngo: {
      name: 'bpmngo',
      grpc: get('bpmngo-grpc', 'localhost:26600')!,
      rest: get('bpmngo-rest', 'http://localhost:9080')!,
      user: get('bpmngo-user', 'admin@bpmngo.com'),
      password: get('bpmngo-password', 'admin123'),
    },
    engines: get('engines', 'camunda,bpmngo')!.split(',').map(s => s.trim()).filter(Boolean),
    only: get('only'),
    out: get('out', path.join('results', stamp))!,
    reference: get('reference'),
    skipApi: argv.includes('--skip-api-checks'),
    resources: !argv.includes('--no-resources'),
    containers: {
      camunda: get('camunda-containers', 'orchestration')!.split(',').map(s => s.trim()).filter(Boolean),
      bpmngo: get('bpmngo-containers', 'bpmngo-app,bpmngo-postgres,bpmngo-redpanda')!.split(',').map(s => s.trim()).filter(Boolean),
    },
  };
}

function loadTraces(dir: string): Record<string, Trace> {
  const out: Record<string, Trace> = {};
  for (const f of fs.readdirSync(dir).filter(f => f.endsWith('.json'))) {
    const t: Trace = JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
    out[t.scenario] = t;
  }
  return out;
}

function loadKnown(): KnownDifference[] {
  const file = path.join(__dirname, '..', 'known-differences.json');
  return fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : [];
}

/** --compare <reference dir> <other dir>: compare saved traces, run nothing. */
function compareOnly(refDir: string, otherDir: string, out: string) {
  const ref = loadTraces(refDir);
  const other = loadTraces(otherDir);
  const known = loadKnown();
  const names = [...new Set([...API_CHECKS, ...SCENARIOS.map(s => s.name), ...Object.keys(ref), ...Object.keys(other)])]
    .filter(n => n in ref || n in other);
  const results = names.map(n => {
    const sc = SCENARIOS.find(s => s.name === n);
    return compareTraces(sc ? sc.group : n.startsWith('api-') ? 'API checks' : 'Other', ref[n], other[n], !!sc?.unordered, known);
  });
  const header = ['BpmnGo ⇄ Camunda 8 behaviour comparison (saved traces)', `Date      : ${new Date().toISOString()}`, `Reference : ${refDir}`, `Compared  : ${otherDir}`];
  const report = textReport(results, 'reference', 'compared', header);
  fs.mkdirSync(out, { recursive: true });
  fs.writeFileSync(path.join(out, 'report.txt'), report);
  console.log(report.split('\n\n\n')[0]);
  console.log(`\nFull report: ${path.join(out, 'report.txt')}`);
  process.exitCode = results.some(r => r.verdict === 'different' || r.verdict === 'missing') ? 1 : 0;
}

async function main() {
  const argv = process.argv.slice(2);
  const ci = argv.indexOf('--compare');
  if (ci >= 0) {
    const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    const oi = argv.indexOf('--out');
    return compareOnly(argv[ci + 1], argv[ci + 2], oi >= 0 ? argv[oi + 1] : path.join('results', `compare-${stamp}`));
  }
  const opt = parseArgs(argv);
  const scenarios = SCENARIOS.filter(s => !opt.only || s.name.includes(opt.only) || s.group.includes(opt.only));
  const runApi = !opt.skipApi && (!opt.only || 'api'.includes(opt.only) || opt.only.startsWith('api'));
  const runTag = 'b' + Date.now().toString(36);
  fs.mkdirSync(opt.out, { recursive: true });

  const engines: Record<string, Engine> = {};
  for (const name of opt.engines) {
    const cfg = name === 'camunda' ? opt.camunda : name === 'bpmngo' ? opt.bpmngo : undefined;
    if (!cfg) throw new Error(`unknown engine "${name}" (use camunda and/or bpmngo)`);
    const e = new Engine(cfg);
    try {
      await e.call('Topology', {}, 10000);
    } catch (err: any) {
      console.error(`✖ ${name}: no answer from the gRPC gateway at ${cfg.grpc} (${err.code}: ${err.message})`);
      process.exit(2);
    }
    engines[name] = e;
    console.log(`✔ ${name}: gRPC ${cfg.grpc}, REST ${cfg.rest}`);
  }

  // ── Record ────────────────────────────────────────────────────────────────
  const traces: Record<string, Record<string, Trace>> = {};
  const usages: EngineUsage[] = [];
  for (const [name, engine] of Object.entries(engines)) {
    traces[name] = {};
    const dir = path.join(opt.out, 'traces', name);
    fs.mkdirSync(dir, { recursive: true });
    console.log(`\n▶ ${name}`);
    let monitor: ResourceMonitor | undefined;
    if (opt.resources) {
      const containers = await checkContainers(name, opt.containers[name] ?? []);
      if (containers.length) {
        monitor = new ResourceMonitor(name, containers);
        await monitor.start();
      }
    }
    const all: Trace[] = [];
    if (runApi) {
      for (const t of await runApiChecks(engine, runTag)) all.push(t);
      console.log(`  api checks: ${API_CHECKS.length}`);
    }
    for (const sc of scenarios) {
      process.stdout.write(`  ${sc.name} … `);
      let t: Trace;
      try {
        t = await runScenario(engine, sc, runTag);
      } catch (e: any) {
        t = {
          scenario: sc.name, engine: name,
          compared: { deploy: 'ok', start: 'ok', jobs: [], actions: [], outcome: `bench error: ${e?.message ?? e}`, finalVariables: null, instances: [] },
          info: { durationMs: 0, errors: [String(e?.stack ?? e)], restNotes: [] },
        };
      }
      console.log(`${t.compared.outcome} (${(t.info.durationMs / 1000).toFixed(1)}s)`);
      all.push(t);
    }
    if (monitor) {
      const u = await monitor.stop();
      usages.push(u);
      console.log(`  resources: CPU ${u.total.cpuSeconds?.toFixed(1) ?? '—'} s, memory peak ${u.total.memPeakMiB.toFixed(0)} MiB`);
    }
    for (const t of all) {
      traces[name][t.scenario] = t;
      fs.writeFileSync(path.join(dir, `${t.scenario}.json`), JSON.stringify(t, null, 2));
    }
  }
  for (const e of Object.values(engines)) e.close();
  const resources = resourcesReport(usages);
  if (usages.length) fs.writeFileSync(path.join(opt.out, 'resources.json'), JSON.stringify(usages, null, 2));

  // ── Compare ───────────────────────────────────────────────────────────────
  // Reference: Camunda from this run, or traces recorded earlier (--reference).
  let refTraces = traces.camunda;
  let refName = 'camunda';
  if (opt.reference) {
    refTraces = loadTraces(opt.reference);
    refName = `camunda (recorded in ${opt.reference})`;
  }
  const other = traces.bpmngo;
  if (!refTraces || !other) {
    if (resources) console.log('\n' + resources);
    console.log(`\nTraces saved in ${opt.out}/traces. Nothing to compare (one engine only, no --reference).`);
    return;
  }

  const known = loadKnown();
  const results: ScenarioResult[] = [];
  const names = [...(runApi ? API_CHECKS : []), ...scenarios.map(s => s.name)];
  for (const n of names) {
    const sc = scenarios.find(s => s.name === n);
    results.push(compareTraces(sc ? sc.group : 'API checks', refTraces[n], other[n], !!sc?.unordered, known));
  }

  const header = [
    'BpmnGo ⇄ Camunda 8 behaviour comparison',
    `Date      : ${new Date().toISOString()}`,
    `Reference : ${refName}  (gRPC ${opt.camunda.grpc}, REST ${opt.camunda.rest})`,
    `Compared  : bpmngo  (gRPC ${opt.bpmngo.grpc}, REST ${opt.bpmngo.rest})`,
  ];
  // The resources go after the verdict list, before the details.
  const [verdicts, ...details] = textReport(results, 'camunda', 'bpmngo', header).split('\n\n\n');
  const head = resources ? verdicts.trimEnd() + '\n\n' + resources : verdicts;
  const report = [head, ...details].join('\n\n\n');
  fs.writeFileSync(path.join(opt.out, 'report.txt'), report);
  fs.writeFileSync(path.join(opt.out, 'summary.json'), JSON.stringify(results.map(r => ({
    scenario: r.scenario, group: r.group, verdict: r.verdict,
    sections: r.sections.filter(s => s.verdict !== 'same').map(s => ({ section: s.section, verdict: s.verdict })),
  })), null, 2));
  console.log('\n' + head);
  console.log(`\nFull report: ${path.join(opt.out, 'report.txt')}`);
  process.exitCode = results.some(r => r.verdict === 'different' || r.verdict === 'missing') ? 1 : 0;
}

main().catch(err => {
  console.error(err);
  process.exit(2);
});
