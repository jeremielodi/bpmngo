/**
 * Suite 57 — Operations and legacy routes  (BPMN: 57_ops.bpmn)
 *
 * The routes no other suite calls: engine info, Prometheus metrics, worker
 * pool metrics, the older (non Camunda 7) task and job routes, timers and
 * multi-instance counters. Each gets at least one call that works and one
 * that must not (unknown id, missing right). Waits are capped at 8 s.
 *
 * Tests:
 *   A) /engine-rest/engine answers; /metrics is Prometheus text with known
 *      counters; /metrics/pool needs CAN_VIEW_METRICS and its reset is a 501.
 *   B) Legacy task routes: /engine-rest/tasks lists the task, /tasks/:id
 *      returns it, /engine-rest/tasks/:id/complete completes it; 404s.
 *   C) Legacy job routes: /jobs?processInstanceId, /jobs/:id and
 *      /engine-rest/jobs/:id; 404s; CAN_READ_JOBS required.
 *   D) /timers lists a pending timer with its due date, /timers/:id returns
 *      it, cancelling the instance removes it; CAN_READ_JOBS required.
 *   E) /multi-instance/execution/:id counts a parallel loop as it runs, and
 *      answers 404 to a user without VIEW on the process.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, waitUntil } from './client';

const BPMN = path.join(__dirname, 'bpmn', '57_ops.bpmn');
const client = new BpmnGoClient();
const ok = { validateStatus: () => true };
const UNKNOWN = '00000000-0000-0000-0000-000000000000';
const runId = Date.now().toString(36);

async function start(key: string) {
  const inst = await client.startProcess(key, {});
  return (inst.processInstanceId ?? inst.id) as string;
}

// A logged-in user with no role beyond the default one.
async function plainUser(tag: string) {
  const email = `ops57-${tag}-${runId}@goflow.com`;
  const res = await client.api.post('/users', { email, full_name: email, password: 'password123' });
  const c = new BpmnGoClient();
  await c.login(email, 'password123');
  return { id: res.data.user.id as string, client: c };
}

async function main() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Ops and Legacy Routes Test');
  const plain = await plainUser('plain');

  await runSuite({
    name: '57 — Operations and legacy routes',
    tests: [
      {
        name: '[A] Engine info, Prometheus metrics, worker pool metrics',
        async fn() {
          const engine = await client.api.get('/engine-rest/engine');
          assert(engine.status === 200 && typeof engine.data.message === 'string', `engine: ${JSON.stringify(engine.data)}`);

          const metrics = await client.api.get('/metrics', { ...ok, responseType: 'text' });
          assert(metrics.status === 200, `/metrics: ${metrics.status}`);
          const text = String(metrics.data);
          assert(text.includes('# HELP') && text.includes('# TYPE'), 'Prometheus exposition format expected');
          assert(text.includes('goflow_worker_pool_queue_length'), 'a known BpmnGo gauge must be exported');

          const pool = await client.api.get('/metrics/pool', ok);
          assert(pool.status === 200 && typeof pool.data.queue_length === 'number' && typeof pool.data.healthy === 'boolean',
            `/metrics/pool: ${pool.status} ${JSON.stringify(pool.data)}`);
          assert((await plain.client.api.get('/metrics/pool', ok)).status === 403, '/metrics/pool needs CAN_VIEW_METRICS');
          const reset = await client.api.post('/metrics/pool/reset', {}, ok);
          assert(reset.status === 501, `reset is not implemented and must say so, got ${reset.status}`);
          assert((await plain.client.api.post('/metrics/pool/reset', {}, ok)).status === 403, 'reset needs CAN_VIEW_METRICS');
        },
      },
      {
        name: '[B] Legacy task routes: list, get, complete; 404 for an unknown task',
        async fn() {
          const id = await start('Ops57');
          const task = await client.waitForTask('o_task', id, 8000);

          const list = (await client.api.get('/engine-rest/tasks', { params: { process_instance_id: id } })).data as any[];
          assert(list.some(t => t.id === task.id), `/engine-rest/tasks must list the task: ${JSON.stringify(list.map(t => t.id))}`);
          const one = await client.api.get(`/tasks/${task.id}`, ok);
          assert(one.status === 200 && one.data.id === task.id && one.data.taskDefinitionKey === 'o_task', `/tasks/:id: ${JSON.stringify(one.data)}`);
          assert((await client.api.get(`/tasks/${UNKNOWN}`, ok)).status === 404, '/tasks/:id unknown: 404');

          // Ops57 is its deployer's: a user without VIEW cannot complete its task.
          const refused = await plain.client.api.post(`/engine-rest/tasks/${task.id}/complete`, { variables: {} }, ok);
          assert(refused.status === 404, `completing without VIEW: expected 404, got ${refused.status}`);
          assert((await client.getTasks(id)).some(t => t.id === task.id), 'a refused completion must leave the task open');

          const done = await client.api.post(`/engine-rest/tasks/${task.id}/complete`, { variables: {} }, ok);
          assert(done.status < 300, `/engine-rest/tasks/:id/complete: ${done.status} ${JSON.stringify(done.data)}`);
          assert(!(await client.getTasks(id)).some(t => t.id === task.id), 'the task must be completed');
          const again = await client.api.post(`/engine-rest/tasks/${UNKNOWN}/complete`, { variables: {} }, ok);
          assert(again.status === 404, `completing an unknown task: expected 404, got ${again.status}`);

          await client.api.delete(`/engine-rest/process-instance/${id}`, ok);
        },
      },
      {
        name: '[C] Legacy job routes: list by instance, get by id; 404; CAN_READ_JOBS',
        async fn() {
          const id = await start('Ops57');
          const jobs = await waitUntil(
            async () => (await client.api.get('/jobs', { params: { processInstanceId: id } })).data as any[],
            list => list.length > 0, 8000, 'the ops57 job');
          assert(jobs.length === 1 && jobs[0].jobType === 'ops57' && jobs[0].status === 'pending', `/jobs: ${JSON.stringify(jobs)}`);

          for (const route of ['/jobs', '/engine-rest/jobs']) {
            const one = await client.api.get(`${route}/${jobs[0].id}`, ok);
            assert(one.status === 200 && one.data.id === jobs[0].id, `${route}/:id: ${one.status} ${JSON.stringify(one.data)}`);
            assert((await client.api.get(`${route}/${UNKNOWN}`, ok)).status === 404, `${route}/:id unknown: 404`);
          }
          assert((await plain.client.api.get('/jobs', ok)).status === 403, '/jobs needs CAN_READ_JOBS');
          await client.api.delete(`/engine-rest/process-instance/${id}`, ok);
        },
      },
      {
        name: '[D] Timers: pending timer listed with its due date, gone once cancelled; CAN_READ_JOBS',
        async fn() {
          const before = Date.now();
          const id = await start('Ops57');
          const timers = await waitUntil(
            async () => (await client.api.get('/timers', { params: { processInstanceId: id } })).data as any[],
            list => list.length > 0, 8000, 'the PT1H timer');
          assert(timers.length === 1 && timers[0].isTriggered === false, `/timers: ${JSON.stringify(timers)}`);
          const due = Date.parse(timers[0].dueAt);
          // PT1H from start, give or take clock skew between host and server.
          assert(Math.abs(due - (before + 3600_000)) < 120_000, `due date ${timers[0].dueAt} should be about 1 h after the start`);

          const one = await client.api.get(`/timers/${timers[0].id}`, ok);
          assert(one.status === 200 && one.data.id === timers[0].id, `/timers/:id: ${one.status}`);
          assert((await client.api.get(`/timers/${UNKNOWN}`, ok)).status === 404, '/timers/:id unknown: 404');
          assert((await plain.client.api.get('/timers', ok)).status === 403, '/timers needs CAN_READ_JOBS');

          await client.api.delete(`/engine-rest/process-instance/${id}`);
          const after = (await client.api.get('/timers', { params: { processInstanceId: id } })).data as any[];
          assert(after.length === 0, `a cancelled instance keeps no timer: ${JSON.stringify(after)}`);
        },
      },
      {
        name: '[E] Multi-instance counters follow a parallel loop; 404 without VIEW',
        async fn() {
          const id = await start('Mi57');
          const tasks = await waitUntil(
            async () => (await client.getTasks(id)).filter(t => t.taskDefinitionKey === 'm_each'),
            list => list.length === 3, 8000, 'three inner tasks');
          const counters = async () => (await client.api.get(`/multi-instance/execution/${id}`)).data.execution;

          let c = await counters();
          assert(c.TotalCount === 3 && c.NrOfCompletedInstances === 0, `at start: ${JSON.stringify(c)}`);
          await client.completeTask(tasks[0].id);
          c = await waitUntil(counters, x => x.NrOfCompletedInstances === 1, 8000, 'one completed inner instance');
          assert(c.NrOfActiveInstances === 2, `one done, two active: ${JSON.stringify(c)}`);

          assert((await client.api.get(`/multi-instance/execution/${UNKNOWN}`, ok)).status === 404, 'unknown instance: 404');
          assert((await client.api.get('/multi-instance/execution/not-a-uuid', ok)).status === 400, 'malformed id: 400');
          // Mi57 is the superuser's (its deployer's): the plain user has no VIEW.
          assert((await plain.client.api.get(`/multi-instance/execution/${id}`, ok)).status === 404, 'without VIEW: 404');

          for (const t of tasks.slice(1)) await client.completeTask(t.id);
          await client.waitForProcessEnd(id, 8000);
        },
      },
    ],
  });
}

main().catch(err => { console.error(err); process.exit(1); });
