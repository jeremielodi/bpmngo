/**
 * Suite 55 — Gateway default flows  (BPMN: 55_gateway_default_flows.bpmn)
 *
 * The BPMN "default" attribute of a gateway names the flow taken only when
 * no other flow's condition is true. It used to be ignored: the default was
 * read as an unconditioned (always true) flow, so an exclusive gateway
 * raised an incident whenever a real condition was also true, and an
 * inclusive gateway took the default in addition to the true branches.
 *
 * Tests:
 *   A) Exclusive: no condition true -> the default flow.
 *   B) Exclusive: a condition true -> that flow, not the default, no incident
 *      (the default is listed first in the model on purpose).
 *   C) Exclusive: two conditions true -> the first in document order
 *      (BPMN 2.0, Camunda 7 and 8).
 *   D) Inclusive: no branch true -> the default flow alone.
 *   E) Inclusive: one or two branches true -> those only, never the default;
 *      the join waits for every activated branch.
 *   F) Inclusive without default: nothing true -> incident on the gateway;
 *      fixing the variable and resolving it runs the gateway again.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN = path.join(__dirname, 'bpmn', '55_gateway_default_flows.bpmn');
const client = new BpmnGoClient();

async function start(key: string, vars: Record<string, unknown>) {
  const inst = await client.startProcess(key, vars);
  return (inst.processInstanceId ?? inst.id) as string;
}

async function openIncidents(id: string) {
  const res = await client.api.get(`/engine-rest/process-instance/${id}/incidents`);
  return (res.data as any[]).filter(i => i.state === 'open');
}

async function waitIncident(id: string, activityId: string) {
  const deadline = Date.now() + 8000;
  while (Date.now() < deadline) {
    const found = (await openIncidents(id)).find(i => i.activityId === activityId);
    if (found) return found;
    await sleep(300);
  }
  throw new Error(`no open incident on ${activityId}`);
}

// Waits until the instance's open tasks are exactly `keys` (any order), then
// a little longer, so a branch created late is not missed.
async function expectOpenTasks(id: string, keys: string[]) {
  const want = [...keys].sort().join(',');
  const deadline = Date.now() + 10000;
  let got = '';
  while (Date.now() < deadline) {
    got = (await client.getTasks(id)).map(t => t.taskDefinitionKey).sort().join(',');
    if (got === want) break;
    await sleep(300);
  }
  await sleep(500);
  got = (await client.getTasks(id)).map(t => t.taskDefinitionKey).sort().join(',');
  assert(got === want, `open tasks: got [${got}], want [${want}]`);
  assert((await openIncidents(id)).length === 0, 'no incident expected');
}

async function finish(id: string) {
  for (const t of await client.getTasks(id)) await client.completeTask(t.id);
  await client.waitForProcessEnd(id, 10000);
}

async function main() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Gateway Default Flows Test');

  await runSuite({
    name: '55 — Gateway default flows',
    tests: [
      {
        name: '[A] Exclusive: no condition true takes the default flow',
        async fn() {
          const id = await start('ExclusiveDefault55', { amount: 50 });
          await expectOpenTasks(id, ['x_small']);
          await finish(id);
        },
      },
      {
        name: '[B] Exclusive: a true condition wins over the default, without incident',
        async fn() {
          const id = await start('ExclusiveDefault55', { amount: 500 });
          await expectOpenTasks(id, ['x_big']);
          await finish(id);
        },
      },
      {
        name: '[C] Exclusive: two true conditions take the first in document order',
        async fn() {
          const id = await start('ExclusiveDefault55', { amount: 5000 });
          await expectOpenTasks(id, ['x_big']);
          await finish(id);
        },
      },
      {
        name: '[D] Inclusive: no branch true takes the default flow alone',
        async fn() {
          const id = await start('InclusiveDefault55', { wantA: false, wantB: false });
          await expectOpenTasks(id, ['i_none']);
          await finish(id);
        },
      },
      {
        name: '[E] Inclusive: true branches only, never the default; the join waits for them all',
        async fn() {
          const one = await start('InclusiveDefault55', { wantA: true, wantB: false });
          await expectOpenTasks(one, ['i_a']);
          await finish(one);

          const two = await start('InclusiveDefault55', { wantA: true, wantB: true });
          await expectOpenTasks(two, ['i_a', 'i_b']);
          const a = (await client.getTasks(two)).find(t => t.taskDefinitionKey === 'i_a')!;
          await client.completeTask(a.id);
          await sleep(500);
          const inst = await client.getProcessInstance(two);
          assert(!!inst && !(inst as any).ended, 'the join must wait for i_b');
          await finish(two);
        },
      },
      {
        name: '[F] Inclusive without default: nothing true is an incident; fix and resolve runs it again',
        async fn() {
          const id = await start('InclusiveNoBranch55', { wantA: false, wantB: false });
          const incident = await waitIncident(id, 'n_fork');
          assert((await client.getTasks(id)).length === 0, 'no branch may start');
          await client.api.post(`/engine-rest/process-instance/${id}/variables`, { wantB: true });
          const res = await client.api.post(`/v2/incidents/${incident.id}/resolution`, {});
          assert(res.status === 204, `resolution: ${res.status}`);
          await expectOpenTasks(id, ['n_b']);
          await finish(id);
        },
      },
    ],
  });
}

main().catch(err => { console.error(err); process.exit(1); });
