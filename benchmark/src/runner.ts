/**
 * runner.ts — plays one scenario on one engine and records a Trace.
 *
 * Everything that is compared between engines goes into the trace's
 * `compared` part; free-text details (error messages, timings) go into
 * `info`, which the report shows but never compares.
 */
import * as fs from 'fs';
import * as path from 'path';
import { ActivatedJob, CallError, Engine } from './engine';
import { Action, JobAnswer, Scenario } from './scenario';

export interface JobRecord {
  instance: string;                 // "main" or "child:<processId>"
  type: string;
  elementId: string;
  retries: number;
  headers: Record<string, unknown>;
  variables: Record<string, unknown>;
  answer: string;                   // what the worker did: complete | fail(n) | error(CODE) | ignore
  result: string;                   // "ok" or the gRPC status code of the answer
}

export interface Trace {
  scenario: string;
  engine: string;
  compared: {
    deploy: string;                 // "ok" or gRPC code
    start: string;                  // "ok" or gRPC code
    jobs: JobRecord[];
    actions: { action: string; result: string }[];
    outcome: string;                // completed | not completed within Ns | start failed
    finalVariables: Record<string, unknown> | null;
    instances: { process: string; state: string; activeElements: string[]; incidents: { elementId: string; errorType: string }[] }[];
  };
  info: {
    durationMs: number;
    errors: string[];
    restNotes: string[];
  };
}

const BPMN_DIR = path.join(__dirname, '..', 'bpmn');

/** Per-run renaming, so instances left over by an earlier run never interfere. */
export function isolate(xml: string, suffix: string): string {
  const ids = new Set<string>();
  for (const m of xml.matchAll(/<(?:\w+:)?process\b[^>]*\bid="([^"]+)"/g)) ids.add(m[1]);
  let out = xml;
  for (const id of ids) {
    out = out.replace(new RegExp(`(<(?:\\w+:)?process\\b[^>]*\\bid=")${escapeRe(id)}"`, 'g'), `$1${id}${suffix}"`);
  }
  // Called process ids, job types, message and signal names.
  out = out.replace(/(\bprocessId=")([^"=][^"]*)"/g, (_, a, v) => `${a}${v}${suffix}"`);
  out = out.replace(/(<zeebe:taskDefinition\b[^>]*\btype=")([^"=][^"]*)"/g, (_, a, v) => `${a}${v}${suffix}"`);
  out = out.replace(/(<(?:\w+:)?(?:message|signal)\b[^>]*\bname=")([^"=][^"]*)"/g, (_, a, v) => `${a}${v}${suffix}"`);
  return out;
}

export async function runScenario(engine: Engine, sc: Scenario, runTagOfBench: string): Promise<Trace> {
  // One suffix per scenario and per run: instances of other scenarios or of
  // earlier runs are never seen.
  const runTag = `${runTagOfBench}${shortHash(sc.name)}`;
  const suffix = `__${runTag}`;
  const t0 = Date.now();
  const trace: Trace = {
    scenario: sc.name,
    engine: engine.cfg.name,
    compared: { deploy: 'ok', start: 'ok', jobs: [], actions: [], outcome: '', finalVariables: null, instances: [] },
    info: { durationMs: 0, errors: [], restNotes: [] },
  };
  const maxMs = sc.maxMs ?? 15000;
  const processIds = new Set<string>();

  // ── Deploy ────────────────────────────────────────────────────────────────
  for (const file of sc.bpmn) {
    const xml = isolate(fs.readFileSync(path.join(BPMN_DIR, file), 'utf8'), suffix);
    for (const m of xml.matchAll(/<(?:\w+:)?process\b[^>]*\bid="([^"]+)"/g)) processIds.add(m[1]);
    try {
      await engine.deploy(file, Buffer.from(xml));
    } catch (e) {
      const err = e as CallError;
      trace.compared.deploy = err.code;
      trace.info.errors.push(`deploy ${file}: ${err.code} ${err.message}`);
    }
  }
  if (trace.compared.deploy !== 'ok') {
    trace.compared.outcome = 'not deployed';
    return finish(trace, t0, runTag);
  }

  // ── Start ─────────────────────────────────────────────────────────────────
  const mainProcess = sc.process + suffix;
  let mainKey: string | undefined;
  let result: { done: boolean; vars?: Record<string, unknown>; error?: CallError } = { done: false };
  const startVars = JSON.stringify(sc.variables ?? {});
  try {
    if (sc.startByMessage) {
      const m = sc.startByMessage;
      await engine.call('PublishMessage', {
        name: m.name + suffix, correlationKey: m.correlationKey ?? '', timeToLive: 0, messageId: '',
        variables: JSON.stringify(m.variables ?? {}), tenantId: '',
      });
    } else if (sc.startBySignal) {
      await engine.call('BroadcastSignal', { signalName: sc.startBySignal.name + suffix, variables: JSON.stringify(sc.startBySignal.variables ?? {}), tenantId: '' });
    } else if (sc.plainStart) {
      const res = await engine.call('CreateProcessInstance', { bpmnProcessId: mainProcess, version: -1, variables: startVars, tenantId: '' });
      mainKey = String(res.processInstanceKey);
    } else {
      engine.call('CreateProcessInstanceWithResult', {
        request: { bpmnProcessId: mainProcess, version: -1, variables: startVars, tenantId: '' },
        requestTimeout: maxMs, fetchVariables: [],
      }, maxMs + 10000).then(
        (res: any) => { mainKey = String(res.processInstanceKey); result = { done: true, vars: JSON.parse(res.variables || '{}') }; },
        (err: CallError) => { result = { done: true, error: err }; },
      );
    }
  } catch (e) {
    const err = e as CallError;
    trace.compared.start = err.code;
    trace.compared.outcome = 'start failed';
    trace.info.errors.push(`start: ${err.code} ${err.message}`);
    return finish(trace, t0, runTag);
  }

  // ── Work jobs and play actions until the instance is over ─────────────────
  const types = Object.keys(sc.workers ?? {});
  const seen: Record<string, number> = {};
  const pending: (Action & { dueAt?: number; fired?: boolean })[] = (sc.actions ?? []).map(a => ({
    ...a,
    dueAt: a.afterMs !== undefined && !a.afterJob ? t0 + a.afterMs : undefined,
  }));
  const deadline = t0 + maxMs;
  const settleMs = 1500; // after the last activity, how long to keep watching

  const deferred: { at: number; job: ActivatedJob; answer: JobAnswer; rec: JobRecord }[] = [];

  let lastActivity = Date.now();
  while (Date.now() < deadline) {
    if (result.done && pending.every(a => a.fired) && deferred.length === 0) break;

    // Deferred answers that are due (the job may have been cancelled meanwhile).
    for (const d of deferred.filter(d => Date.now() >= d.at)) {
      deferred.splice(deferred.indexOf(d), 1);
      d.rec.result = await answerJob(engine, d.job, d.answer, trace);
      lastActivity = Date.now();
    }

    const batches = await Promise.all(types.map(t => engine.activateJobs(t + suffix, 32, 300).catch((err: CallError) => {
      trace.info.errors.push(`activate ${t}: ${err.code} ${err.message}`);
      return [] as ActivatedJob[];
    })));
    for (const job of batches.flat()) {
      lastActivity = Date.now();
      const type = job.type.slice(0, job.type.length - suffix.length);
      if (!mainKey && job.bpmnProcessId === mainProcess) mainKey = job.processInstanceKey;
      seen[type] = (seen[type] ?? 0) + 1;
      const answer = sc.workers![type](job.variables, { nth: seen[type] });
      const rec: JobRecord = {
        instance: job.bpmnProcessId === mainProcess ? 'main' : `child:${job.bpmnProcessId}`,
        type, elementId: job.elementId, retries: job.retries, headers: job.customHeaders, variables: job.variables,
        answer: describe(answer), result: 'ok',
      };
      if ('later' in answer) {
        rec.result = 'pending';
        deferred.push({ at: Date.now() + answer.later, job, answer: answer.then, rec });
      } else {
        rec.result = await answerJob(engine, job, answer, trace);
      }
      trace.compared.jobs.push(rec);
      for (const a of pending) {
        if (!a.fired && a.afterJob === type && (a.nth ?? 1) === seen[type]) a.dueAt = Date.now() + (a.afterMs ?? defaultDelay(a));
      }
    }

    for (const a of pending) {
      if (a.fired || a.dueAt === undefined || Date.now() < a.dueAt) continue;
      a.fired = true;
      lastActivity = Date.now();
      trace.compared.actions.push({ action: describeAction(a), result: await playAction(engine, a, suffix, mainKey, mainProcess, trace) });
    }

    // Starts that do not return a result: done once nothing moves any more.
    if (!result.done && (sc.plainStart || sc.startByMessage || sc.startBySignal)) {
      if (pending.every(a => a.fired) && deferred.length === 0 && Date.now() - lastActivity > settleMs && trace.compared.jobs.length + trace.compared.actions.length > 0) {
        const st = await instanceStates(engine, mainProcess);
        if (st.length > 0 && st.every(s => s !== 'ACTIVE')) break;
      }
    }
    if (types.length === 0) await sleep(200);
  }

  // ── Outcome ───────────────────────────────────────────────────────────────
  if (result.done && result.vars) {
    trace.compared.outcome = 'completed';
    trace.compared.finalVariables = result.vars;
  } else if (result.done && result.error) {
    trace.compared.outcome = result.error.code === 'DEADLINE_EXCEEDED' ? `not completed within ${maxMs / 1000}s` : `result call failed: ${result.error.code}`;
    if (result.error.code !== 'DEADLINE_EXCEEDED') trace.info.errors.push(`result: ${result.error.code} ${result.error.message}`);
  } else if (sc.plainStart || sc.startByMessage || sc.startBySignal) {
    trace.compared.outcome = 'no result requested';
  } else {
    trace.compared.outcome = `not completed within ${maxMs / 1000}s`;
  }

  // ── End state, as the REST API reports it ─────────────────────────────────
  trace.compared.instances = await snapshot(engine, [...processIds], suffix, trace, trace.compared.outcome === 'completed' ? mainProcess : undefined);

  // ── Clean up whatever still runs (best effort) ────────────────────────────
  if (mainKey && trace.compared.instances.some(i => i.state === 'ACTIVE')) {
    await engine.call('CancelProcessInstance', { processInstanceKey: mainKey }).catch(() => undefined);
  }
  return finish(trace, t0, runTag);
}

async function answerJob(engine: Engine, job: ActivatedJob, answer: JobAnswer, trace: Trace): Promise<string> {
  try {
    if ('complete' in answer) {
      await engine.call('CompleteJob', { jobKey: job.key, variables: JSON.stringify(answer.complete) });
    } else if ('fail' in answer) {
      await engine.call('FailJob', {
        jobKey: job.key, retries: answer.fail.retries, errorMessage: answer.fail.message ?? 'failed by bench',
        retryBackOff: answer.fail.backoffMs ?? 0, variables: JSON.stringify(answer.fail.variables ?? {}),
      });
    } else if ('error' in answer) {
      await engine.call('ThrowError', {
        jobKey: job.key, errorCode: answer.error.code, errorMessage: answer.error.message ?? '',
        variables: JSON.stringify(answer.error.variables ?? {}),
      });
    }
    return 'ok';
  } catch (e) {
    const err = e as CallError;
    trace.info.errors.push(`answer ${job.type} at ${job.elementId}: ${err.code} ${err.message}`);
    return err.code;
  }
}

async function playAction(engine: Engine, a: Action, suffix: string, mainKey: string | undefined, mainProcess: string, trace: Trace): Promise<string> {
  try {
    if (a.publish) {
      const p = a.publish;
      const res = await engine.call('PublishMessage', {
        name: p.name + suffix, correlationKey: p.correlationKey ?? '', timeToLive: p.ttlMs ?? 0,
        messageId: p.messageId ? p.messageId + suffix : '', variables: JSON.stringify(p.variables ?? {}), tenantId: '',
      });
      return res ? 'ok' : 'ok';
    }
    if (a.signal) {
      await engine.call('BroadcastSignal', { signalName: a.signal.name + suffix, variables: JSON.stringify(a.signal.variables ?? {}), tenantId: '' });
      return 'ok';
    }
    if (a.setVariables) {
      if (!mainKey) return 'no instance key';
      await engine.call('SetVariables', { elementInstanceKey: mainKey, variables: JSON.stringify(a.setVariables.variables), local: !!a.setVariables.local });
      return 'ok';
    }
    if (a.cancel) {
      if (!mainKey) return 'no instance key';
      await engine.call('CancelProcessInstance', { processInstanceKey: mainKey });
      return 'ok';
    }
    if (a.completeUserTask) {
      return await completeUserTask(engine, mainProcess, a.completeUserTask.elementId, a.completeUserTask.variables ?? {}, trace);
    }
    return 'nothing to do';
  } catch (e) {
    const err = e as CallError;
    trace.info.errors.push(`${describeAction(a)}: ${err.code} ${err.message}`);
    return err.code;
  }
}

/** Completes the open user task at elementId via REST (/v2/user-tasks), waiting for it to show up. */
async function completeUserTask(engine: Engine, process: string, elementId: string, variables: Record<string, unknown>, trace: Trace): Promise<string> {
  const keys = await instanceKeys(engine, process);
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    for (const k of keys.length ? keys : await instanceKeys(engine, process)) {
      const res = await engine.rest('POST', '/v2/user-tasks/search', { filter: { processInstanceKey: k } });
      const task = (res.data?.items ?? []).find((t: any) => t.elementId === elementId && /^(CREATED|ACTIVE|created)$/i.test(String(t.state ?? 'CREATED')));
      if (task) {
        const taskKey = task.userTaskKey ?? task.key ?? task.id;
        const done = await engine.rest('POST', `/v2/user-tasks/${taskKey}/completion`, { variables });
        if (done.status >= 300) trace.info.errors.push(`complete user task ${elementId}: HTTP ${done.status} ${JSON.stringify(done.data).slice(0, 200)}`);
        return done.status < 300 ? 'ok' : `HTTP ${done.status}`;
      }
    }
    await sleep(500);
  }
  return 'user task not found';
}

/** States of every instance of a process, via the REST search. */
async function instanceStates(engine: Engine, process: string): Promise<string[]> {
  const res = await engine.rest('POST', '/v2/process-instances/search', { filter: { processDefinitionId: process } });
  return (res.data?.items ?? []).map((i: any) => String(i.state));
}

async function instanceKeys(engine: Engine, process: string): Promise<string[]> {
  const res = await engine.rest('POST', '/v2/process-instances/search', { filter: { processDefinitionId: process } });
  return (res.data?.items ?? []).map((i: any) => String(i.processInstanceKey ?? i.key));
}

/**
 * End state of every instance of the scenario's processes. The REST data of
 * some engines (Camunda's secondary storage) is filled asynchronously: read
 * until two reads agree, and — when gRPC already reported the main instance
 * completed — until the search shows it finished too.
 */
async function snapshot(engine: Engine, processIds: string[], suffix: string, trace: Trace, completedProcess?: string): Promise<Trace['compared']['instances']> {
  let previous = '';
  let current: Trace['compared']['instances'] = [];
  const deadline = Date.now() + 30000;
  const mainName = completedProcess?.slice(0, completedProcess.length - suffix.length);
  while (Date.now() < deadline) {
    current = [];
    for (const pid of processIds) {
      const res = await engine.rest('POST', '/v2/process-instances/search', { filter: { processDefinitionId: pid } });
      if (res.status >= 300) {
        note(trace, `process-instances/search: HTTP ${res.status}`);
        continue;
      }
      for (const inst of res.data?.items ?? []) {
        const key = String(inst.processInstanceKey ?? inst.key);
        const state = String(inst.state);
        const entry = { process: pid.slice(0, pid.length - suffix.length), state, activeElements: [] as string[], incidents: [] as { elementId: string; errorType: string }[] };
        if (state === 'ACTIVE') {
          entry.activeElements = await activeElements(engine, key, trace);
          entry.incidents = await openIncidents(engine, key, trace);
        }
        current.push(entry);
      }
    }
    current.sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    const now = JSON.stringify(current);
    const mainSettled = !mainName || current.some(i => i.process === mainName && i.state !== 'ACTIVE');
    if (now === previous && current.length > 0 && mainSettled) break;
    previous = now;
    await sleep(1000);
  }
  if (mainName && !current.some(i => i.process === mainName && i.state !== 'ACTIVE')) {
    note(trace, 'REST search still showed the completed instance as active after 30 s');
  }
  return current;
}

async function activeElements(engine: Engine, key: string, trace: Trace): Promise<string[]> {
  // Camunda 8.8+: element-instances; BpmnGo: flownode-instances (older name).
  let res = await engine.rest('POST', '/v2/element-instances/search', { filter: { processInstanceKey: key }, page: { limit: 100 } });
  if (res.status === 404) {
    note(trace, '/v2/element-instances/search: 404, used /v2/flownode-instances/search');
    res = await engine.rest('POST', '/v2/flownode-instances/search', { filter: { processInstanceKey: key } });
  }
  if (res.status >= 300) {
    note(trace, `element search: HTTP ${res.status}`);
    return [];
  }
  // The root PROCESS element is left out; a multi-instance activity listed
  // twice (its body and itself, same elementId) counts once.
  const ids = (res.data?.items ?? [])
    .filter((i: any) => String(i.state).toUpperCase() === 'ACTIVE')
    .filter((i: any) => String(i.type ?? '') !== 'PROCESS')
    .map((i: any) => String(i.elementId ?? i.flowNodeId));
  return [...new Set<string>(ids)].sort();
}

async function openIncidents(engine: Engine, key: string, trace: Trace) {
  const res = await engine.rest('POST', '/v2/incidents/search', { filter: { processInstanceKey: key } });
  if (res.status >= 300) {
    note(trace, `incidents/search: HTTP ${res.status}`);
    return [];
  }
  return (res.data?.items ?? [])
    .filter((i: any) => /^(ACTIVE|OPEN|PENDING)$/i.test(String(i.state ?? 'ACTIVE')))
    .map((i: any) => ({ elementId: String(i.elementId ?? i.flowNodeId ?? i.activityId), errorType: String(i.errorType ?? i.incidentType ?? '') }))
    .sort((a: any, b: any) => (a.elementId + a.errorType).localeCompare(b.elementId + b.errorType));
}

function note(trace: Trace, s: string) {
  if (!trace.info.restNotes.includes(s)) trace.info.restNotes.push(s);
}

function finish(trace: Trace, t0: number, runTag: string): Trace {
  trace.info.durationMs = Date.now() - t0;
  // Drop the per-run suffix everywhere, so traces of different runs compare.
  return JSON.parse(JSON.stringify(trace).split(`__${runTag}`).join(''));
}

function describe(a: JobAnswer): string {
  if ('later' in a) return `after ${a.later} ms: ${describe(a.then)}`;
  if ('complete' in a) return 'complete';
  if ('fail' in a) return `fail(retries=${a.fail.retries})`;
  if ('error' in a) return `error(${a.error.code})`;
  return 'ignore';
}

function describeAction(a: Action): string {
  const when = a.afterJob ? `after job ${a.afterJob}${a.nth && a.nth > 1 ? ` #${a.nth}` : ''}` : `after ${a.afterMs ?? 0} ms`;
  if (a.publish) return `${when}: publish ${a.publish.name}${a.publish.correlationKey ? ` key=${a.publish.correlationKey}` : ''}${a.publish.ttlMs ? ` ttl=${a.publish.ttlMs}` : ''}${a.publish.messageId ? ` id=${a.publish.messageId}` : ''}`;
  if (a.signal) return `${when}: signal ${a.signal.name}`;
  if (a.setVariables) return `${when}: set variables ${JSON.stringify(a.setVariables.variables)}`;
  if (a.cancel) return `${when}: cancel instance`;
  if (a.completeUserTask) return `${when}: complete user task ${a.completeUserTask.elementId}`;
  return `${when}: nothing`;
}

/** Signals and keyless messages are not buffered: give the engine time to reach the catch event. */
function defaultDelay(a: Action): number {
  return a.signal || a.publish ? 1500 : 300;
}

const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));

function shortHash(s: string): string {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return (h >>> 0).toString(36);
}

function escapeRe(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
