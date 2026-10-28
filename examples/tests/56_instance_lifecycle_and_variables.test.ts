/**
 * Suite 56 — Ending/stopping an instance, its state, and single variables
 * (BPMN: 56_instance_lifecycle.bpmn)
 *
 * Lifecycle56 waits on a user task, an external job, a message and a timer
 * at once, so ending it shows whether each of them is released.
 *
 * Tests:
 *   A) POST /end closes everything (task cancelled in history, job no longer
 *      fetchable, timer gone, message no longer correlates), status
 *      completed, business variables kept; ending twice is a 404.
 *      These used to only flip the status: the task stayed "created" in
 *      history forever and called instances kept running.
 *   B) POST /stop: same cleanup, status terminated, the reason is kept.
 *   C) DELETE (cancel) keeps the business variables — it used to replace
 *      them all with its three "terminated*" variables.
 *   D) GET /state: running, completed, terminated, unknown.
 *   E) Single variables: PUT then GET keeps the type; DELETE removes it, and
 *      a gateway reading it then takes its default flow; unknown -> 404.
 *   F) VIEW without MANAGE: can read a variable, cannot write, delete, end
 *      or stop (404, as for any instance it may not manage).
 *   G) /variable-instance and /count: filters, count = list length, and
 *      per-instance grants respected.
 *   H) Async start: 202 then the instance appears; unknown key -> 404.
 */
import * as path from 'path';
import * as fs from 'fs';
import FormData from 'form-data';
import { BpmnGoClient, runSuite, assert, sleep, waitUntil } from './client';

const BPMN = path.join(__dirname, 'bpmn', '56_instance_lifecycle.bpmn');
const client = new BpmnGoClient();
const ok = { validateStatus: () => true };
const runId = Date.now().toString(36);

async function start(key: string, variables: Record<string, unknown> = {}) {
  const inst = await client.startProcess(key, variables);
  return (inst.processInstanceId ?? inst.id) as string;
}

// Lifecycle56 started and waiting on all four of its waits.
async function startWaitingEverywhere(variables: Record<string, unknown> = {}) {
  const id = await start('Lifecycle56', variables);
  await client.waitForTask('l_task', id, 10000);
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const jobs = (await client.api.get('/jobs', { params: { processInstanceId: id } })).data ?? [];
    const timers = (await client.api.get('/timers', { params: { processInstanceId: id } })).data ?? [];
    if (jobs.length > 0 && timers.length > 0) return id;
    await sleep(300);
  }
  throw new Error('Lifecycle56 never reached its job and timer');
}

async function historicTasks(id: string) {
  return (await client.api.get('/engine-rest/history/task', { params: { processInstanceId: id } })).data as any[];
}

// What must hold once an instance is over, however it was closed.
async function assertReleased(id: string) {
  assert((await client.getTasks(id)).length === 0, 'no open task may remain');
  const tasks = await historicTasks(id);
  assert(tasks.length === 1 && (tasks[0].taskState ?? tasks[0].status) === 'cancelled',
    `the open task must be recorded as cancelled, got ${JSON.stringify(tasks)}`);
  const locked = await client.fetchAndLock([{ name: 'lifecycle56' }]);
  assert(!locked.some(j => j.processInstanceId === id), 'its job must not be fetchable any more');
  const timers = (await client.api.get('/timers', { params: { processInstanceId: id } })).data ?? [];
  assert(timers.length === 0, `its timer must be gone, got ${JSON.stringify(timers)}`);
  const msg = await client.api.post('/engine-rest/message', { messageName: 'lifecycle56_msg', processInstanceId: id }, ok);
  assert(msg.status >= 400, `the message must no longer correlate, got ${msg.status}`);
}

async function state(id: string) {
  return client.api.get(`/engine-rest/process-instance/${id}/state`, ok);
}

async function vars(id: string) {
  return (await client.api.get(`/engine-rest/process-instance/${id}/variables`)).data as Record<string, any>;
}

async function createUser(email: string) {
  const superuser = client;
  const res = await superuser.api.post('/users', { email, full_name: email, password: 'password123' });
  const c = new BpmnGoClient();
  await c.login(email, 'password123');
  return { id: res.data.user.id as string, client: c };
}

// Deploys the model with Variables56 renamed, for a process this suite can
// restrict without affecting the others.
async function deployRenamed(key: string) {
  const xml = fs.readFileSync(BPMN, 'utf8').replace(/Lifecycle56/g, `L_${key}`).replace(/Variables56/g, key);
  const form = new FormData();
  form.append('resources', Buffer.from(xml), { filename: `${key}.bpmn` });
  form.append('deployment-name', key);
  await client.api.post('/engine-rest/deployment/create', form, { headers: form.getHeaders() });
}

async function main() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Instance Lifecycle Test');

  await runSuite({
    name: '56 — Ending/stopping instances, state, single variables',
    tests: [
      {
        name: '[A] /end releases task, job, timer and message; completed; variables kept; twice -> 404',
        async fn() {
          const id = await startWaitingEverywhere({ customer: 'ACME' });
          const res = await client.api.post(`/engine-rest/process-instance/${id}/end`, { variables: { closedBy: 'test' } }, ok);
          assert(res.status === 200, `end: ${res.status} ${JSON.stringify(res.data)}`);
          await assertReleased(id);
          const s = (await state(id)).data;
          assert(s.status === 'completed' && s.ended === true, `state after end: ${JSON.stringify(s)}`);
          const v = await vars(id);
          assert(v.customer?.value === 'ACME' && v.closedBy?.value === 'test' && v.ended?.value === true,
            `business and end variables must both be kept: ${JSON.stringify(v)}`);
          const again = await client.api.post(`/engine-rest/process-instance/${id}/end`, {}, ok);
          assert(again.status === 404, `ending an ended instance: expected 404, got ${again.status}`);
        },
      },
      {
        name: '[B] /stop releases everything too; terminated; the reason is kept',
        async fn() {
          const id = await startWaitingEverywhere({ customer: 'ACME' });
          const res = await client.api.post(`/engine-rest/process-instance/${id}/stop`, { reason: 'customer left' }, ok);
          assert(res.status === 200 && res.data.reason === 'customer left', `stop: ${res.status} ${JSON.stringify(res.data)}`);
          await assertReleased(id);
          const s = (await state(id)).data;
          assert(s.status === 'terminated' && s.ended === true, `state after stop: ${JSON.stringify(s)}`);
          const v = await vars(id);
          assert(v.customer?.value === 'ACME' && v.stopReason?.value === 'customer left', `variables after stop: ${JSON.stringify(v)}`);
          const again = await client.api.post(`/engine-rest/process-instance/${id}/stop`, {}, ok);
          assert(again.status === 404, `stopping a stopped instance: expected 404, got ${again.status}`);
        },
      },
      {
        name: '[C] Cancelling (DELETE) keeps the business variables',
        async fn() {
          const id = await startWaitingEverywhere({ customer: 'ACME', amount: 42 });
          await client.api.delete(`/engine-rest/process-instance/${id}`);
          await assertReleased(id);
          const v = await vars(id);
          assert(v.customer?.value === 'ACME' && v.amount?.value === 42 && v.terminated?.value === true,
            `variables after cancel: ${JSON.stringify(v)}`);

          // Once archived, closing it again is a 404 — this used to panic
          // (a nil instance) and take the whole server down.
          await waitUntil(async () => (await state(id)).data.ended, e => e === true, 8000, 'the cancelled instance');
          await sleep(1500); // let the background archiver move it
          for (const op of ['delete', 'end', 'stop']) {
            const r = op === 'delete'
              ? await client.api.delete(`/engine-rest/process-instance/${id}`, ok)
              : await client.api.post(`/engine-rest/process-instance/${id}/${op}`, {}, ok);
            assert(r.status === 404, `${op} on an archived instance: expected 404, got ${r.status}`);
          }
          assert((await client.api.get('/health')).status === 200, 'the server must still be up');
        },
      },
      {
        name: '[D] /state: running, completed, terminated, unknown',
        async fn() {
          const id = await start('Variables56');
          await client.waitForTask('v_wait', id, 10000);
          const running = (await state(id)).data;
          assert(running.status === 'running' && running.ended === false && running.activeExecutions >= 1,
            `running state: ${JSON.stringify(running)}`);
          const unknown = await state('00000000-0000-0000-0000-000000000000');
          assert(unknown.status === 404, `unknown instance: expected 404, got ${unknown.status}`);
          const bad = await state('not-a-uuid');
          assert(bad.status === 400, `malformed id: expected 400, got ${bad.status}`);
          await client.api.delete(`/engine-rest/process-instance/${id}`);
          assert((await state(id)).data.status === 'terminated', 'state after cancel');
        },
      },
      {
        name: '[E] Single variables: typed PUT/GET, DELETE changes the gateway outcome, unknown -> 404',
        async fn() {
          const id = await start('Variables56', { approved: true });
          const wait = await client.waitForTask('v_wait', id, 10000);
          const url = (name: string) => `/engine-rest/process-instance/${id}/variables/${name}`;

          const typed: Array<[string, any, string]> = [
            ['label', 'hello', 'String'],
            ['flag', false, 'Boolean'],
            ['order', { lines: [1, 2], total: 3.5 }, 'Object'],
          ];
          for (const [name, value, type] of typed) {
            const put = await client.api.put(url(name), { value, type }, ok);
            assert(put.status === 204, `PUT ${name}: ${put.status}`);
            const got = (await client.api.get(url(name))).data;
            assert(JSON.stringify(got.value) === JSON.stringify(value) && got.type === type,
              `GET ${name}: ${JSON.stringify(got)}, want ${JSON.stringify(value)} as ${type}`);
          }
          const num = (await client.api.put(url('count'), { value: 7, type: 'Integer' }, ok)).status;
          assert(num === 204, `PUT count: ${num}`);
          assert((await client.api.get(url('count'))).data.value === 7, 'a number reads back as the same number');

          assert((await client.api.get(url(`nope_${runId}`), ok)).status === 404, 'unknown variable: 404');

          const del = await client.api.delete(url('approved'), ok);
          assert(del.status === 204, `DELETE: ${del.status}`);
          assert((await client.api.get(url('approved'), ok)).status === 404, 'a deleted variable is gone');
          assert((await vars(id)).label?.value === 'hello', 'deleting one variable keeps the others');

          // approved is gone: the gateway's condition is not true -> default.
          await client.completeTask(wait.id);
          const next = await client.waitForTask('v_no', id, 10000);
          assert(!(await client.getTasks(id)).some(t => t.taskDefinitionKey === 'v_yes'), 'v_yes must not be taken');
          await client.completeTask(next.id);
          await client.waitForProcessEnd(id, 10000);
        },
      },
      {
        name: '[F] VIEW without MANAGE: reads a variable, cannot write, delete, end or stop',
        async fn() {
          const key = `Vars56_${runId}`;
          await deployRenamed(key);
          const { id: viewerId, client: viewer } = await createUser(`vars56-viewer-${runId}@goflow.com`);
          await client.api.post(`/engine-rest/process-definition/key/${key}/permissions`, { granteeType: 'user', granteeId: viewerId, permission: 'VIEW' });

          const id = await start(key, { approved: true });
          await client.waitForTask('v_wait', id, 10000);
          const url = `/engine-rest/process-instance/${id}/variables/approved`;

          assert((await viewer.api.get(url, ok)).data.value === true, 'VIEW can read');
          const put = await viewer.api.put(url, { value: false, type: 'Boolean' }, ok);
          assert(put.status === 404, `VIEW PUT: expected 404, got ${put.status}`);
          const del = await viewer.api.delete(url, ok);
          assert(del.status === 404, `VIEW DELETE: expected 404, got ${del.status}`);
          for (const op of ['end', 'stop']) {
            const r = await viewer.api.post(`/engine-rest/process-instance/${id}/${op}`, {}, ok);
            assert(r.status === 404, `VIEW ${op}: expected 404, got ${r.status}`);
          }
          assert((await client.api.get(url)).data.value === true, 'refused writes must change nothing');
          assert((await state(id)).data.status === 'running', 'refused end/stop must leave it running');
          await client.api.delete(`/engine-rest/process-instance/${id}`);
        },
      },
      {
        name: '[G] /variable-instance and /count: filters, count = length, grants respected',
        async fn() {
          const key = `VarQ56_${runId}`;
          await deployRenamed(key);
          const a = await start(key, { [`q56_alpha_${runId}`]: 1, [`q56_beta_${runId}`]: 'x' });
          const b = await start(key, { [`q56_alpha_${runId}`]: 2 });
          await client.waitForTask('v_wait', a, 10000);
          await client.waitForTask('v_wait', b, 10000);

          const list = async (params: Record<string, string>, c = client) =>
            (await c.api.get('/engine-rest/variable-instance', { params })).data as any[];
          const count = async (params: Record<string, string>, c = client) =>
            (await c.api.get('/engine-rest/variable-instance/count', { params })).data.count as number;

          const byName = await list({ variableName: `q56_alpha_${runId}` });
          assert(byName.length === 2 && byName.every(v => v.name === `q56_alpha_${runId}`), `variableName: ${JSON.stringify(byName)}`);
          assert(await count({ variableName: `q56_alpha_${runId}` }) === 2, 'count matches variableName');

          const like = await list({ variableNameLike: `q56_%_${runId}` });
          assert(like.length === 3, `variableNameLike: ${like.map(v => v.name)}`);
          assert(await count({ variableNameLike: `q56_%_${runId}` }) === 3, 'count matches variableNameLike');

          const inA = await list({ processInstanceIdIn: a, variableNameLike: `q56_%_${runId}` });
          assert(inA.length === 2 && inA.every(v => v.processInstanceId === a), `processInstanceIdIn: ${JSON.stringify(inA)}`);

          // POST body form gives the same answer.
          const posted = (await client.api.post('/engine-rest/variable-instance', { processInstanceIdIn: [b], variableNameLike: `q56_%_${runId}` })).data;
          assert(posted.length === 1 && posted[0].value === 2, `POST query: ${JSON.stringify(posted)}`);

          // Restricted process, user without a grant: sees none of them.
          const { client: outsider } = await createUser(`varq56-out-${runId}@goflow.com`);
          const { id: insiderId } = await createUser(`varq56-in-${runId}@goflow.com`);
          await client.api.post(`/engine-rest/process-definition/key/${key}/permissions`, { granteeType: 'user', granteeId: insiderId, permission: 'VIEW' });
          assert((await list({ variableNameLike: `q56_%_${runId}` }, outsider)).length === 0, 'no grant: no variables');
          assert(await count({ variableNameLike: `q56_%_${runId}` }, outsider) === 0, 'no grant: count 0');

          for (const id of [a, b]) await client.api.delete(`/engine-rest/process-instance/${id}`);
        },
      },
      {
        name: '[H] Async start: 202 then the instance appears; unknown key -> 404',
        async fn() {
          const marker = `async-${runId}`;
          const res = await client.api.post('/async/process-definitions/Variables56/start', { variables: { marker } }, ok);
          assert(res.status === 202, `async start: ${res.status} ${JSON.stringify(res.data)}`);
          const deadline = Date.now() + 15000;
          let found: any;
          while (!found && Date.now() < deadline) {
            const rows = (await client.api.get('/engine-rest/variable-instance', { params: { variableName: 'marker' } })).data as any[];
            found = rows.find(r => r.value === marker);
            if (!found) await sleep(300);
          }
          assert(!!found, 'the queued start must create the instance');
          await client.api.delete(`/engine-rest/process-instance/${found.processInstanceId}`);

          const unknown = await client.api.post(`/async/process-definitions/nope_${runId}/start`, { variables: {} }, ok);
          assert(unknown.status === 404, `unknown key: expected 404, got ${unknown.status}`);
        },
      },
    ],
  });
}

main().catch(err => { console.error(err); process.exit(1); });
