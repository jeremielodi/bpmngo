/**
 * apichecks.ts — checks of the API surface itself, independent of BPMN
 * semantics: does a client that works against Camunda 8 keep working?
 * Each check produces a Trace like a scenario, so it is compared the same way.
 */
import { Engine } from './engine';
import { isolate, Trace } from './runner';

const SIMPLE = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="api" targetNamespace="bench">
  <bpmn:process id="api_simple" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>
    <bpmn:serviceTask id="work"><bpmn:extensionElements><zeebe:taskDefinition type="api-work"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/>
    <bpmn:endEvent id="end"/>
  </bpmn:process>
</bpmn:definitions>`;

const TIMER = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="api_timer" targetNamespace="bench">
  <bpmn:process id="api_timer" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:timeDuration>PT3S</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="wait" targetRef="end"/>
    <bpmn:endEvent id="end"/>
  </bpmn:process>
</bpmn:definitions>`;

export const API_CHECKS = [
  'api-deploy-response',
  'api-grpc-key-usable-in-rest',
  'api-element-instances-endpoint',
  'api-finished-instance-searchable',
  'api-timer-waits-its-duration',
];

export async function runApiChecks(engine: Engine, runTag: string): Promise<Trace[]> {
  const suffix = `__${runTag}`;
  const traces: Trace[] = [];
  const mk = (name: string, outcome: string, details: Record<string, unknown>, errors: string[] = []): Trace => ({
    scenario: name,
    engine: engine.cfg.name,
    compared: { deploy: 'ok', start: 'ok', jobs: [], actions: [], outcome, finalVariables: details as any, instances: [] },
    info: { durationMs: 0, errors, restNotes: [] },
  });

  // 1. What DeployResource answers (resource name echoed, version).
  const pid = 'api_simple' + suffix;
  let deployed: any;
  try {
    deployed = await engine.deploy('api-simple.bpmn', Buffer.from(isolate(SIMPLE, suffix)));
    const p = deployed.deployments?.[0]?.process ?? {};
    traces.push(mk('api-deploy-response', 'ok', { resourceName: p.resourceName, version: p.version, bpmnProcessId: String(p.bpmnProcessId).split(suffix).join('') }));
  } catch (e: any) {
    traces.push(mk('api-deploy-response', `deploy failed: ${e.code}`, {}, [e.message]));
    return traces;
  }

  // 2. The key CreateProcessInstance returns works in the REST API.
  let key = '';
  try {
    const res = await engine.call('CreateProcessInstance', { bpmnProcessId: pid, version: -1, variables: JSON.stringify({ probe: 1 }), tenantId: '' });
    key = String(res.processInstanceKey);
    let rest = await engine.rest('GET', `/v2/process-instances/${key}`);
    for (let i = 0; i < 10 && rest.status === 404; i++) {      // secondary storage may lag
      await sleep(1000);
      rest = await engine.rest('GET', `/v2/process-instances/${key}`);
    }
    traces.push(mk('api-grpc-key-usable-in-rest', rest.status < 300 ? 'ok' : `HTTP ${rest.status}`, {
      sameKeyInRest: rest.status < 300 && String(rest.data?.processInstanceKey) === key,
    }, rest.status < 300 ? [] : [JSON.stringify(rest.data).slice(0, 200)]));
  } catch (e: any) {
    traces.push(mk('api-grpc-key-usable-in-rest', `start failed: ${e.code}`, {}, [e.message]));
  }

  // 3. The element-instance search of Camunda 8.8+ exists.
  const el = await engine.rest('POST', '/v2/element-instances/search', { filter: {}, page: { limit: 1 } });
  traces.push(mk('api-element-instances-endpoint', el.status < 300 ? 'ok' : `HTTP ${el.status}`, {}));

  // 4. Once the instance is over, its variables and elements can still be searched.
  try {
    let jobs: any[] = [];
    for (let i = 0; i < 20 && !jobs.length; i++) jobs = await engine.activateJobs('api-work' + suffix, 1, 500);
    if (jobs[0]) await engine.call('CompleteJob', { jobKey: jobs[0].key, variables: JSON.stringify({ done: true }) });
    await sleep(2000);
    const search = await engine.rest('POST', '/v2/process-instances/search', { filter: { processDefinitionId: pid } });
    const inst = (search.data?.items ?? [])[0];
    const restKey = inst ? String(inst.processInstanceKey) : '';
    let vars = 0;
    let elements = 0;
    for (let i = 0; i < 10; i++) {
      const v = await engine.rest('POST', '/v2/variables/search', { filter: { processInstanceKey: restKey } });
      vars = (v.data?.items ?? []).length;
      let e = await engine.rest('POST', '/v2/element-instances/search', { filter: { processInstanceKey: restKey } });
      if (e.status === 404) e = await engine.rest('POST', '/v2/flownode-instances/search', { filter: { processInstanceKey: restKey } });
      elements = (e.data?.items ?? []).length;
      if (vars > 0 && elements > 0) break;
      await sleep(1000);
    }
    traces.push(mk('api-finished-instance-searchable', inst ? String(inst.state) : 'instance not found', {
      variablesFound: vars > 0,
      elementsFound: elements > 0,
    }));
  } catch (e: any) {
    traces.push(mk('api-finished-instance-searchable', `failed: ${e.code}`, {}, [e.message]));
  }

  // 5. A PT3S timer really waits about 3 s. Catches clock/time-zone mistakes
  //    (e.g. a database not in UTC making every timer fire at once).
  try {
    await engine.deploy('api-timer.bpmn', Buffer.from(isolate(TIMER, suffix)));
    const t0 = Date.now();
    await engine.call('CreateProcessInstanceWithResult', {
      request: { bpmnProcessId: 'api_timer' + suffix, version: -1, variables: '{}', tenantId: '' },
      requestTimeout: 15000, fetchVariables: [],
    }, 25000);
    const s = (Date.now() - t0) / 1000;
    traces.push(mk('api-timer-waits-its-duration', 'completed', {
      waited: s < 2.5 ? 'less than 2.5 s (fired early)' : s <= 6 ? 'about 3 s' : 'more than 6 s (fired late)',
    }, [`waited ${s.toFixed(1)} s for a PT3S timer`]));
  } catch (e: any) {
    traces.push(mk('api-timer-waits-its-duration', `failed: ${e.code}`, {}, [e.message]));
  }

  return JSON.parse(JSON.stringify(traces).split(suffix).join(''));
}

const sleep = (ms: number) => new Promise(r => setTimeout(r, ms));
