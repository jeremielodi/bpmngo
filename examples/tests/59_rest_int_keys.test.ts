/**
 * Suite 59 — int64 keys in /v2 answers  (BPMN: 59_rest_int_keys.bpmn)
 *
 * Runs only when the server answers /v2 with int64 keys: start the stack
 * with REST_KEYS=int and run this suite with REST_KEYS=int in the
 * environment. Otherwise it reports itself skipped.
 *
 * Tests:
 *   A) The key gRPC returns is the key /v2 returns, as a string; the UUID
 *      still works in a path (the frontend sends UUIDs).
 *   B) A job activated over /v2 is completed with its int64 jobKey; a user
 *      task found by the instance's int64 key is completed with its int64
 *      userTaskKey; the instance ends.
 *   C) Search answers carry int64 keys, variables are left as they are.
 *   D) A job failed with its int64 key raises an incident whose incidentKey
 *      and jobKey are int64 keys; the incident is resolved with its key; a
 *      user task is assigned and unassigned with its key.
 *   E) A message publication answers with int64 keys (its key, the
 *      correlated instances); a REST deployment with the int64 keys of the
 *      deployment and of its definitions (the same as gRPC's).
 */
import * as path from 'path';
import * as fs from 'fs';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';
import FormData from 'form-data';
import { BpmnGoClient, runSuite, assert, sleep, SKIPPED_EXIT_CODE } from './client';
import { authInterceptor } from './grpc_client';

const BPMN_FILE = path.join(__dirname, 'bpmn', '59_rest_int_keys.bpmn');
const GRPC_ADDR = process.env.GRPC_ADDR ?? '127.0.0.1:26500';
const rest = new BpmnGoClient();
const isIntKey = (k: unknown) => typeof k === 'string' && /^\d+$/.test(k);

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

async function start(gw: any, variables: object): Promise<string> {
  const res = await call(gw, 'CreateProcessInstance', { bpmnProcessId: 'p59_keys', version: -1, variables: JSON.stringify(variables), tenantId: '' });
  return String(res.processInstanceKey);
}

async function activateJob(instanceKey: string): Promise<any> {
  for (let i = 0; i < 30; i++) {
    const res = await rest.api.post('/v2/jobs/activation', { type: 'p59-work', worker: 'suite59', timeout: 30000, maxJobsToActivate: 10 });
    const job = (res.data.jobs as any[]).find(j => j.processInstanceKey === instanceKey);
    if (job) return job;
    await sleep(200);
  }
  throw new Error(`no p59-work job for instance ${instanceKey}`);
}

async function createdTask(instanceKey: string): Promise<any> {
  for (let i = 0; i < 30; i++) {
    const res = await rest.api.post('/v2/user-tasks/search', { filter: { processInstanceKey: instanceKey, state: 'CREATED' } });
    const task = (res.data.items as any[])[0];
    if (task) return task;
    await sleep(200);
  }
  throw new Error(`no user task for instance ${instanceKey}`);
}

async function run() {
  if ((process.env.REST_KEYS ?? '').toLowerCase() !== 'int') {
    console.log('\n  ⏭️  59 — int64 keys in /v2: skipped (set REST_KEYS=int on the server and for this suite)\n');
    process.exit(SKIPPED_EXIT_CODE); // reported as skipped by run_all, not as passed
  }
  await rest.loginAsSuperUser();
  const gw = officialGateway();
  await call(gw, 'DeployResource', { resources: [{ name: '59_rest_int_keys.bpmn', content: fs.readFileSync(BPMN_FILE) }], tenantId: '' });

  try {
    await runSuite({
      name: '59 — int64 keys in /v2 answers',
      tests: [
        {
          name: '[A] gRPC and /v2 show the same key; a UUID still works in a path',
          async fn() {
            const key = await start(gw, {});
            const got = await rest.api.get(`/v2/process-instances/${key}`);
            assert(got.data.processInstanceKey === key, `/v2 key ${JSON.stringify(got.data.processInstanceKey)}, gRPC key ${key}`);
            assert(isIntKey(got.data.processDefinitionKey), `processDefinitionKey: ${JSON.stringify(got.data.processDefinitionKey)}`);

            const live = (await rest.api.get('/engine-rest/process-instance', { params: { processKey: 'p59_keys' } })).data as any[];
            const uuid = live.find(i => i.status !== 'completed')?.id;
            assert(!!uuid, 'the instance is listed by /engine-rest with its UUID');
            const byUUID = await rest.api.get(`/v2/process-instances/${uuid}`, { validateStatus: () => true });
            assert(byUUID.status === 200 && isIntKey(byUUID.data.processInstanceKey), `GET by UUID: ${byUUID.status} ${JSON.stringify(byUUID.data)}`);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: key });

            // Created over REST: int64 keys too, usable in the instance's paths.
            const created = (await rest.api.post('/v2/process-instances', { processDefinitionId: 'p59_keys', variables: {} })).data;
            assert(isIntKey(created.processInstanceKey) && isIntKey(created.processDefinitionKey), `create: ${JSON.stringify(created)}`);
            await activateJob(created.processInstanceKey); // the token now waits at k_work
            const moved = await rest.api.post(`/v2/process-instances/${created.processInstanceKey}/modification`,
              { moveInstructions: [{ sourceElementId: 'k_work', targetElementId: 'k_review' }] }, { validateStatus: () => true });
            assert(moved.status === 200 && moved.data.processInstanceKey === created.processInstanceKey, `modification: ${moved.status} ${JSON.stringify(moved.data)}`);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: created.processInstanceKey });

            // A token that went through k_work: step-back by the instance's int64 key.
            const walked = (await rest.api.post('/v2/process-instances', { processDefinitionId: 'p59_keys', variables: {} })).data;
            const job = await activateJob(walked.processInstanceKey);
            await rest.api.post(`/v2/jobs/${job.jobKey}/completion`, { variables: {} });
            await createdTask(walked.processInstanceKey);
            const back = await rest.api.post(`/v2/process-instances/${walked.processInstanceKey}/step-back`,
              { elementId: 'k_review', dryRun: true }, { validateStatus: () => true });
            assert(back.status === 200 && back.data.toElementId === 'k_work', `step-back dry run: ${back.status} ${JSON.stringify(back.data)}`);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: walked.processInstanceKey });
          },
        },
        {
          name: '[B] Job and user task completed over /v2 with their int64 keys',
          async fn() {
            const key = await start(gw, { order: 'O-59' });

            let job: any;
            for (let i = 0; i < 30 && !job; i++) {
              const res = await rest.api.post('/v2/jobs/activation', { type: 'p59-work', worker: 'suite59', timeout: 30000, maxJobsToActivate: 10 });
              job = (res.data.jobs as any[]).find(j => j.processInstanceKey === key);
              if (!job) await sleep(200);
            }
            assert(!!job, `no p59-work job for instance ${key}`);
            assert(isIntKey(job.jobKey), `jobKey: ${JSON.stringify(job.jobKey)}`);
            assert(job.variables?.order === 'O-59', `job variables: ${JSON.stringify(job.variables)}`);
            const done = await rest.api.post(`/v2/jobs/${job.jobKey}/completion`, { variables: { worked: true } }, { validateStatus: () => true });
            assert(done.status < 300, `job completion: HTTP ${done.status} ${JSON.stringify(done.data)}`);

            let task: any;
            for (let i = 0; i < 30 && !task; i++) {
              const res = await rest.api.post('/v2/user-tasks/search', { filter: { processInstanceKey: key, state: 'CREATED' } });
              task = (res.data.items as any[])[0];
              if (!task) await sleep(200);
            }
            assert(!!task && isIntKey(task.userTaskKey) && task.processInstanceKey === key, `user task: ${JSON.stringify(task)}`);
            const completed = await rest.api.post(`/v2/user-tasks/${task.userTaskKey}/completion`, { variables: { approved: true } }, { validateStatus: () => true });
            assert(completed.status < 300, `user task completion: HTTP ${completed.status} ${JSON.stringify(completed.data)}`);

            let state = '';
            for (let i = 0; i < 30 && state !== 'COMPLETED'; i++) {
              state = (await rest.api.get(`/v2/process-instances/${key}`)).data.state;
              if (state !== 'COMPLETED') await sleep(200);
            }
            assert(state === 'COMPLETED', `instance state: ${state}`);
          },
        },
        {
          name: '[C] Searches answer with int64 keys; variables are untouched',
          async fn() {
            const someUUID = '05f280f3-0bed-424a-b4c0-1b7c0e88f3e9';
            const key = await start(gw, { ref: someUUID, big: 9007199254740993 });

            const vars = (await rest.api.post('/v2/variables/search', { filter: { processInstanceKey: key } })).data.items as any[];
            assert(vars.length > 0 && vars.every(v => v.processInstanceKey === key), `variables keys: ${JSON.stringify(vars)}`);
            assert(vars.find(v => v.name === 'ref')?.value === someUUID, 'a variable holding a UUID is not rewritten');

            const insts = (await rest.api.post('/v2/process-instances/search', { filter: { processDefinitionId: 'p59_keys' } })).data.items as any[];
            assert(insts.length > 0 && insts.every(i => isIntKey(i.processInstanceKey)), `instance search keys: ${JSON.stringify(insts.map(i => i.processInstanceKey))}`);
            assert(insts.some(i => i.processInstanceKey === key), 'the gRPC key is found by the search');

            const els = (await rest.api.post('/v2/element-instances/search', { filter: { processInstanceKey: key } })).data.items as any[];
            assert(els.length > 0 && els.every(e => isIntKey(e.flowNodeInstanceKey) && e.processInstanceKey === key), `element keys: ${JSON.stringify(els)}`);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: key });
          },
        },
        {
          name: '[D] Job failure, incident resolution, user task assignment with int64 keys',
          async fn() {
            const key = await start(gw, {});
            const job = await activateJob(key);
            const failed = await rest.api.post(`/v2/jobs/${job.jobKey}/failure`, { retries: 0, errorMessage: 'permanent' }, { validateStatus: () => true });
            assert(failed.status < 300, `failure: HTTP ${failed.status} ${JSON.stringify(failed.data)}`);

            let incident: any;
            for (let i = 0; i < 30 && !incident; i++) {
              incident = ((await rest.api.post('/v2/incidents/search', { filter: { processInstanceKey: key } })).data.items as any[])[0];
              if (!incident) await sleep(200);
            }
            assert(!!incident && isIntKey(incident.incidentKey) && incident.jobKey === job.jobKey && incident.processInstanceKey === key,
              `incident keys: ${JSON.stringify(incident)} (job ${job.jobKey})`);
            await rest.api.post(`/v2/jobs/${job.jobKey}/failure`, { retries: 1 }, { validateStatus: () => true });
            const resolved = await rest.api.post(`/v2/incidents/${incident.incidentKey}/resolution`, {}, { validateStatus: () => true });
            assert(resolved.status === 204, `resolution: HTTP ${resolved.status} ${JSON.stringify(resolved.data)}`);

            const retry = await activateJob(key);
            const done = await rest.api.post(`/v2/jobs/${retry.jobKey}/completion`, { variables: {} }, { validateStatus: () => true });
            assert(done.status < 300, `completion after resolution: HTTP ${done.status}`);

            const task = await createdTask(key);
            const assigned = await rest.api.post(`/v2/user-tasks/${task.userTaskKey}/assignment`, { assignee: 'dana' }, { validateStatus: () => true });
            assert(assigned.status < 300, `assignment: HTTP ${assigned.status} ${JSON.stringify(assigned.data)}`);
            const mine = ((await rest.api.post('/v2/user-tasks/search', { filter: { processInstanceKey: key } })).data.items as any[])[0];
            assert(mine?.assignee === 'dana' && mine.userTaskKey === task.userTaskKey, `after assignment: ${JSON.stringify(mine)}`);
            const unassigned = await rest.api.delete(`/v2/user-tasks/${task.userTaskKey}/assignee`, { validateStatus: () => true });
            assert(unassigned.status < 300, `unassignment: HTTP ${unassigned.status} ${JSON.stringify(unassigned.data)}`);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: key });
          },
        },
        {
          name: '[E] Message publication and REST deployment answer with int64 keys',
          async fn() {
            const waiting = String((await call(gw, 'CreateProcessInstance', {
              bpmnProcessId: 'p59_msg', version: -1, variables: JSON.stringify({ orderId: 'O-59' }), tenantId: '',
            })).processInstanceKey);
            let pub: any;
            for (let i = 0; i < 30; i++) {
              pub = (await rest.api.post('/v2/messages/publication', { name: 'p59-msg', correlationKey: 'O-59', variables: {} })).data;
              if ((pub.correlatedProcessInstanceKeys ?? []).length) break;
              await sleep(200); // the subscription may not be open yet
            }
            assert(isIntKey(pub.key), `message key: ${JSON.stringify(pub.key)}`);
            assert(JSON.stringify(pub.correlatedProcessInstanceKeys) === JSON.stringify([waiting]) && pub.processInstanceKey === waiting,
              `correlated: ${JSON.stringify(pub)} (want ${waiting})`);

            const form = new FormData();
            form.append('resources', fs.createReadStream(BPMN_FILE));
            const dep = (await rest.api.post('/v2/resources/deployments', form, { headers: form.getHeaders() })).data;
            const def = dep.deployedProcessDefinitions?.p59_keys;
            assert(isIntKey(dep.id) && isIntKey(def?.id), `deployment keys: ${JSON.stringify(dep)}`);
            // The same definition key as gRPC's, for the same version.
            const viaGrpc = await call(gw, 'CreateProcessInstance', { bpmnProcessId: 'p59_keys', version: -1, variables: '{}', tenantId: '' });
            assert(String(viaGrpc.processDefinitionKey) === def.id, `gRPC definition key ${viaGrpc.processDefinitionKey}, REST ${def.id}`);
            await call(gw, 'CancelProcessInstance', { processInstanceKey: String(viaGrpc.processInstanceKey) });
          },
        },
      ],
    });
  } finally {
    gw.close();
  }
}

run().catch(err => { console.error(err); process.exit(1); });
