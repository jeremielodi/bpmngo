/**
 * latency.ts — how long one call takes, without load: each call is made
 * alone, one after the other, on both engines.
 *
 *   npm run latency -- [--iterations 30] [--repeat 2] [engine options as for npm run compare]
 *
 * Calls measured: Topology; DeployResource (a new process each time);
 * CreateProcessInstance; CreateProcessInstanceWithResult (start → end);
 * creation → job (from CreateProcessInstance until ActivateJobs hands the
 * job out); CompleteJob. The report gives p50/p95 per call and per pass.
 */
import * as fs from 'fs';
import * as path from 'path';
import { Engine, EngineConfig } from './engine';

function args(argv: string[]) {
  const get = (name: string, def?: string) => {
    const i = argv.indexOf(`--${name}`);
    return i >= 0 ? argv[i + 1] : (process.env[name.toUpperCase().replace(/-/g, '_')] ?? def);
  };
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
  const engines: Record<string, EngineConfig> = {
    camunda: { name: 'camunda', grpc: get('camunda-grpc', 'localhost:26500')!, rest: get('camunda-rest', 'http://localhost:8080')!, user: get('camunda-user'), password: get('camunda-password') },
    bpmngo: { name: 'bpmngo', grpc: get('bpmngo-grpc', 'localhost:26600')!, rest: get('bpmngo-rest', 'http://localhost:9080')!, user: get('bpmngo-user', 'admin@goflow.com'), password: get('bpmngo-password', 'admin123') },
  };
  return {
    engines,
    run: get('engines', 'camunda,bpmngo')!.split(',').map(s => s.trim()).filter(Boolean),
    iterations: Number(get('iterations', '30')),
    repeat: Math.max(1, Number(get('repeat', '2'))),
    out: get('out', path.join('results', `latency-${stamp}`))!,
  };
}

const CALLS = ['Topology', 'DeployResource', 'CreateProcessInstance', 'CreateWithResult', 'Creation → job', 'CompleteJob'] as const;
type Call = typeof CALLS[number];

const oneTask = (id: string) => `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="${id}_d" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="${id}" isExecutable="true">
    <bpmn:startEvent id="s"/><bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="t"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="${id}-work"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:sequenceFlow id="f1" sourceRef="t" targetRef="e"/><bpmn:endEvent id="e"/>
  </bpmn:process>
</bpmn:definitions>`;

const straight = (id: string) => `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="${id}_d" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="${id}" isExecutable="true">
    <bpmn:startEvent id="s"/><bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="e"/><bpmn:endEvent id="e"/>
  </bpmn:process>
</bpmn:definitions>`;

const now = () => Number(process.hrtime.bigint()) / 1e6;

async function timed<T>(samples: number[], f: () => Promise<T>): Promise<T> {
  const t = now();
  const r = await f();
  samples.push(now() - t);
  return r;
}

async function measure(cfg: EngineConfig, iterations: number): Promise<Record<Call, number[]>> {
  const engine = new Engine(cfg);
  const tag = `lat_${Date.now().toString(36)}`;
  const samples = Object.fromEntries(CALLS.map(c => [c, [] as number[]])) as Record<Call, number[]>;
  await engine.deploy(`${tag}.bpmn`, Buffer.from(oneTask(tag)));
  await engine.deploy(`${tag}_s.bpmn`, Buffer.from(straight(`${tag}_s`)));
  for (let i = 0; i < iterations; i++) {
    await timed(samples.Topology, () => engine.call('Topology', {}));
    const id = `${tag}_d${i}`;
    await timed(samples.DeployResource, () => engine.deploy(`${id}.bpmn`, Buffer.from(straight(id))));
    await timed(samples.CreateWithResult, () => engine.call('CreateProcessInstanceWithResult', {
      request: { bpmnProcessId: `${tag}_s`, version: -1, variables: '{}', tenantId: '' }, requestTimeout: 30000, fetchVariables: [],
    }, 40000));
    const created = now();
    await timed(samples.CreateProcessInstance, () => engine.call('CreateProcessInstance', { bpmnProcessId: tag, version: -1, variables: '{}' }));
    let jobs = await engine.activateJobs(`${tag}-work`, 1, 5000);
    for (let tries = 0; !jobs.length && tries < 5; tries++) jobs = await engine.activateJobs(`${tag}-work`, 1, 5000);
    if (!jobs.length) throw new Error(`${cfg.name}: the job of iteration ${i} never came`);
    samples['Creation → job'].push(now() - created);
    await timed(samples.CompleteJob, () => engine.call('CompleteJob', { jobKey: jobs[0].key, variables: '{}' }));
  }
  engine.close();
  return samples;
}

const pct = (v: number[], p: number) => {
  const s = [...v].sort((a, b) => a - b);
  return s.length ? s[Math.min(s.length - 1, Math.floor(p * s.length))] : NaN;
};

async function main() {
  const o = args(process.argv.slice(2));
  fs.mkdirSync(o.out, { recursive: true });
  const passes: Record<string, Record<Call, number[]>>[] = [];
  for (let p = 0; p < o.repeat; p++) {
    const byEngine: Record<string, Record<Call, number[]>> = {};
    for (const name of p % 2 ? [...o.run].reverse() : o.run) {
      const cfg = o.engines[name];
      if (!cfg) throw new Error(`unknown engine "${name}"`);
      process.stdout.write(`▶ ${name}, pass ${p + 1}/${o.repeat} … `);
      byEngine[name] = await measure(cfg, o.iterations);
      console.log('done');
    }
    passes.push(byEngine);
  }
  const lines = [
    'BpmnGo ⇄ Camunda 8 latency per call',
    `Date       : ${new Date().toISOString()}`,
    `Iterations : ${o.iterations} sequential calls of each kind, per pass; ${o.repeat} passes (engine order alternating)`,
    '',
    `  ${'call'.padEnd(22)}${o.run.map(e => `${e} p50 / p95 (ms), each pass`.padStart(36)).join('')}`,
  ];
  for (const call of CALLS) {
    lines.push(`  ${call.padEnd(22)}` + o.run.map(e =>
      passes.map(p => `${pct(p[e][call], 0.5).toFixed(2)} / ${pct(p[e][call], 0.95).toFixed(2)}`).join(' · ').padStart(36)).join(''));
  }
  lines.push('', 'Each call alone, one after the other, no load. Creation → job: from CreateProcessInstance until');
  lines.push('ActivateJobs hands the job out.');
  const text = lines.join('\n');
  fs.writeFileSync(path.join(o.out, 'latency.json'), JSON.stringify(passes, null, 2));
  fs.writeFileSync(path.join(o.out, 'latency-report.txt'), text);
  console.log('\n' + text);
}

main().catch(err => {
  console.error(err);
  process.exit(2);
});
