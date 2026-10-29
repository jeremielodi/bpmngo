/**
 * Suite 58 — Camunda 8 parity  (BPMN: 58_camunda_parity.bpmn)
 *
 * What the BpmnGo / Camunda 8.10 comparison bench found different, driven
 * like a Camunda 8 client: grpc-js on zeebe-node's official zeebe.proto,
 * plus the /v2 REST API.
 *
 * Tests:
 *   A) The int64 key gRPC returns is usable in /v2 (GET, variables and
 *      element searches).
 *   B) FailJob variables are local to the task: the retry sees them, the
 *      instance does not keep them.
 *   C) ThrowError variables are local to the catching boundary: dropped
 *      without output mappings, carried on by them.
 *   D) A multi-instance outputElement variable stays in each iteration.
 *   E) CompleteJob on a job cancelled with its instance: NOT_FOUND.
 *   F) /v2/element-instances/search (8.8+ name) exists, works without a
 *      filter, and finds a finished instance's elements and variables.
 *   G) DeployResource answers with the file name the client sent.
 *   H) Incident types: Camunda 8's in /v2 (errorType), the Camunda 7 era
 *      names in /engine-rest (incidentType).
 */
import * as path from 'path';
import * as fs from 'fs';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';
import { BpmnGoClient, runSuite, assert, sleep } from './client';
import { authInterceptor } from './grpc_client';

const BPMN_FILE = path.join(__dirname, 'bpmn', '58_camunda_parity.bpmn');
const RESOURCE_NAME = 'suite-58-parity.bpmn';
const GRPC_ADDR = process.env.GRPC_ADDR ?? '127.0.0.1:26500';
const rest = new BpmnGoClient();

function officialGateway(): any {
  const def = protoLoader.loadSync(path.join(__dirname, 'node_modules', 'zeebe-node', 'proto', 'zeebe.proto'), {
    keepCase: true, longs: String, enums: String, defaults: true, oneofs: true,
  });
  const proto = grpc.loadPackageDefinition(def) as any;
  return new proto.gateway_protocol.Gateway(GRPC_ADDR, grpc.credentials.createInsecure(), { interceptors: [authInterceptor()] });
}

function call<T = any>(client: any, method: string, req: unknown): Promise<T> {
  return new Promise((resolve, reject) => client[method](req, (err: grpc.ServiceError | null, res: T) => (err ? reject(err) : resolve(res))));
}

async function start(gw: any, bpmnProcessId: string, variables: object = {}): Promise<string> {
  const res = await call(gw, 'CreateProcessInstance', { bpmnProcessId, version: -1, variables: JSON.stringify(variables), tenantId: '' });
  return String(res.processInstanceKey);
}

/** The jobs of one instance, activated with a poll until `count` arrive. */
async function activate(gw: any, type: string, instanceKey: string, count = 1): Promise<any[]> {
  const found: any[] = [];
  const deadline = Date.now() + 15000;
  while (found.length < count && Date.now() < deadline) {
    const jobs = await new Promise<any[]>((resolve, reject) => {
      const out: any[] = [];
      const stream = gw.ActivateJobs({ type, worker: 'suite58', timeout: 30000, maxJobsToActivate: 10, requestTimeout: 1000, fetchVariable: [], tenantIds: [] });
      stream.on('data', (r: any) => out.push(...(r.jobs ?? [])));
      stream.on('end', () => resolve(out));
      stream.on('error', reject);
    });
    found.push(...jobs.filter(j => String(j.processInstanceKey) === instanceKey));
    if (found.length < count) await sleep(200);
  }
  assert(found.length >= count, `expected ${count} ${type} job(s) for instance ${instanceKey}, got ${found.length}`);
  return found;
}

const varsOf = (job: any) => JSON.parse(job.variables || '{}');

/** The instance's variables over /v2, live or archived. */
async function v2Variables(instanceKey: string): Promise<Record<string, unknown>> {
  const res = await rest.api.post('/v2/variables/search', { filter: { processInstanceKey: instanceKey } });
  return Object.fromEntries((res.data.items as any[]).map(v => [v.name, v.value]));
}

async function waitEnded(instanceKey: string) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const res = await rest.api.get(`/v2/process-instances/${instanceKey}`, { validateStatus: () => true });
    if (res.status === 200 && res.data.state !== 'ACTIVE') return res.data;
    await sleep(200);
  }
  throw new Error(`instance ${instanceKey} did not end`);
}

async function run() {
  await rest.loginAsSuperUser();
  const gw = officialGateway();
  const deployed = await call(gw, 'DeployResource', {
    resources: [{ name: RESOURCE_NAME, content: fs.readFileSync(BPMN_FILE) }], tenantId: '',
  });

  try {
    await runSuite({
      name: '58 — Camunda 8 parity',
      tests: [
        {
          name: '[A] The gRPC key works in /v2',
          async fn() {
            const key = await start(gw, 'p58_work', { probe: 1 });
            const got = await rest.api.get(`/v2/process-instances/${key}`, { validateStatus: () => true });
            assert(got.status === 200, `GET /v2/process-instances/${key}: HTTP ${got.status}`);
            const vars = await v2Variables(key);
            assert(vars.probe === 1, `variables search by gRPC key: ${JSON.stringify(vars)}`);
            const el = await rest.api.post('/v2/element-instances/search', { filter: { processInstanceKey: key } });
            assert(el.data.items.some((e: any) => e.elementId === 'w_task'), `element search by gRPC key: ${JSON.stringify(el.data)}`);
            const unknown = await rest.api.get('/v2/process-instances/123456789', { validateStatus: () => true });
            assert(unknown.status === 404, `unknown numeric key: HTTP ${unknown.status}, want 404`);
            const [job] = await activate(gw, 'p58-work', key);
            await call(gw, 'CompleteJob', { jobKey: job.key, variables: '{}' });
          },
        },
        {
          name: '[B] FailJob variables: seen by the retry, not kept by the instance',
          async fn() {
            const key = await start(gw, 'p58_work');
            const [first] = await activate(gw, 'p58-work', key);
            assert(varsOf(first).lastError === undefined, 'first attempt sees no lastError');
            await call(gw, 'FailJob', { jobKey: first.key, retries: 2, errorMessage: 'temporary', retryBackOff: 0, variables: JSON.stringify({ lastError: 'E1' }) });
            const [retry] = await activate(gw, 'p58-work', key);
            assert(varsOf(retry).lastError === 'E1', `the retry should see lastError: ${retry.variables}`);
            await call(gw, 'CompleteJob', { jobKey: retry.key, variables: JSON.stringify({ done: true }) });
            await waitEnded(key);
            const vars = await v2Variables(key);
            assert(vars.done === true && !('lastError' in vars), `final variables: ${JSON.stringify(vars)}`);
          },
        },
        {
          name: '[C] ThrowError variables: local to the boundary, carried on only by its output mappings',
          async fn() {
            const plain = await start(gw, 'p58_error');
            const [pay1] = await activate(gw, 'p58-pay', plain);
            await call(gw, 'ThrowError', { jobKey: pay1.key, errorCode: 'PLAIN', errorMessage: 'declined', variables: JSON.stringify({ declinedBy: 'bank' }) });
            const [after1] = await activate(gw, 'p58-after-error', plain);
            assert(!('declinedBy' in varsOf(after1)), `no output mapping: declinedBy should be dropped: ${after1.variables}`);
            await call(gw, 'CompleteJob', { jobKey: after1.key, variables: '{}' });

            const mapped = await start(gw, 'p58_error');
            const [pay2] = await activate(gw, 'p58-pay', mapped);
            await call(gw, 'ThrowError', { jobKey: pay2.key, errorCode: 'MAPPED', errorMessage: 'declined', variables: JSON.stringify({ declinedBy: 'bank' }) });
            const [after2] = await activate(gw, 'p58-after-error', mapped);
            const v = varsOf(after2);
            assert(v.reason === 'bank' && !('declinedBy' in v), `output mapping: want reason=bank only, got ${after2.variables}`);
            await call(gw, 'CompleteJob', { jobKey: after2.key, variables: '{}' });
          },
        },
        {
          name: '[D] Multi-instance outputElement stays in each iteration',
          async fn() {
            const key = await start(gw, 'p58_mi', { items: [3, 1, 2] });
            const jobs = await activate(gw, 'p58-each', key, 3);
            for (const job of jobs) {
              const v = varsOf(job);
              assert(v.result === null, `each iteration starts with a local result = null: ${job.variables}`);
              await call(gw, 'CompleteJob', { jobKey: job.key, variables: JSON.stringify({ result: v.item * 10 }) });
            }
            const [after] = await activate(gw, 'p58-after-mi', key);
            const v = varsOf(after);
            assert(JSON.stringify(v.results) === '[30,10,20]', `results in input order: ${after.variables}`);
            assert(!('result' in v), `result must not reach the process scope: ${after.variables}`);
            await call(gw, 'CompleteJob', { jobKey: after.key, variables: '{}' });
          },
        },
        {
          name: '[E] CompleteJob on a cancelled job: NOT_FOUND',
          async fn() {
            const key = await start(gw, 'p58_work');
            const [job] = await activate(gw, 'p58-work', key);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: key });
            let code = -1;
            try {
              await call(gw, 'CompleteJob', { jobKey: job.key, variables: '{}' });
            } catch (e: any) {
              code = e.code;
            }
            assert(code === grpc.status.NOT_FOUND, `want NOT_FOUND (${grpc.status.NOT_FOUND}), got ${code}`);
          },
        },
        {
          name: '[F] element-instances search: no filter, and a finished instance',
          async fn() {
            const any = await rest.api.post('/v2/element-instances/search', { filter: {}, page: { limit: 1 } }, { validateStatus: () => true });
            assert(any.status === 200 && Array.isArray(any.data.items), `no filter: HTTP ${any.status}`);
            assert(any.data.items.length <= 1, `page.limit 1: got ${any.data.items.length}`);

            const key = await start(gw, 'p58_work', { kept: 'yes' });
            const [job] = await activate(gw, 'p58-work', key);
            await call(gw, 'CompleteJob', { jobKey: job.key, variables: '{}' });
            await waitEnded(key);
            // Archived in the background: wait until its executions are gone.
            await sleep(1500);
            const el = await rest.api.post('/v2/element-instances/search', { filter: { processInstanceKey: key } });
            const ids = (el.data.items as any[]).map(e => `${e.elementId}:${e.state}`);
            assert(ids.includes('w_task:COMPLETED'), `finished instance's elements: ${JSON.stringify(ids)}`);
            const vars = await v2Variables(key);
            assert(vars.kept === 'yes', `finished instance's variables: ${JSON.stringify(vars)}`);
          },
        },
        {
          name: '[G] DeployResource answers with the file name sent',
          async fn() {
            const names = (deployed.deployments as any[]).map(d => d.process?.resourceName);
            assert(names.length === 4 && names.every(n => n === RESOURCE_NAME), `resource names: ${JSON.stringify(names)}`);
          },
        },
        {
          name: '[H] Incident types: Camunda 8 names in /v2, Camunda 7 era names in /engine-rest',
          async fn() {
            const cases: { key: string; element: string; v2: string; c7: string }[] = [];

            cases.push({ key: await start(gw, 'p58_gw', { x: 1 }), element: 'g_gw', v2: 'CONDITION_ERROR', c7: 'executionError' });
            cases.push({ key: await start(gw, 'p58_mi', { items: 'not a list' }), element: 'm_each', v2: 'EXTRACT_VALUE_ERROR', c7: 'executionError' });

            const noRetries = await start(gw, 'p58_work');
            const [job] = await activate(gw, 'p58-work', noRetries);
            await call(gw, 'FailJob', { jobKey: job.key, retries: 0, errorMessage: 'permanent', retryBackOff: 0, variables: '' });
            cases.push({ key: noRetries, element: 'w_task', v2: 'JOB_NO_RETRIES', c7: 'failedExternalTask' });

            const uncaught = await start(gw, 'p58_error');
            const [pay] = await activate(gw, 'p58-pay', uncaught);
            await call(gw, 'ThrowError', { jobKey: pay.key, errorCode: 'NOBODY', errorMessage: 'uncaught', variables: '' });
            cases.push({ key: uncaught, element: 'e_pay', v2: 'UNHANDLED_ERROR_EVENT', c7: 'failedExternalTask' });

            for (const c of cases) {
              let items: any[] = [];
              const deadline = Date.now() + 10000;
              while (!items.length && Date.now() < deadline) {
                items = (await rest.api.post('/v2/incidents/search', { filter: { processInstanceKey: c.key } })).data.items;
                if (!items.length) await sleep(200);
              }
              assert(items.length === 1 && items[0].elementId === c.element, `${c.v2}: incidents ${JSON.stringify(items)}`);
              assert(items[0].errorType === c.v2, `/v2 errorType: want ${c.v2}, got ${items[0].errorType}`);

              const uuid = (await rest.api.get(`/v2/process-instances/${c.key}`)).data.processInstanceKey;
              const c7 = (await rest.api.get('/engine-rest/incident', { params: { processInstanceId: uuid } })).data as any[];
              assert(c7.length === 1 && c7[0].incidentType === c.c7 && c7[0].errorType === c.v2,
                `/engine-rest: want ${c.c7} / ${c.v2}, got ${JSON.stringify(c7.map(i => [i.incidentType, i.errorType]))}`);
              const filtered = (await rest.api.get('/engine-rest/incident', { params: { processInstanceId: uuid, incidentType: c.c7 } })).data as any[];
              assert(filtered.length === 1, `/engine-rest incidentType=${c.c7} filter: ${filtered.length}`);

              await call(gw, 'CancelProcessInstance', { processInstanceKey: c.key });
            }
          },
        },
      ],
    });
  } finally {
    gw.close();
  }
}

run().catch(err => { console.error(err); process.exit(1); });
