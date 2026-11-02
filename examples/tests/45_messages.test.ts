/**
 * Suite 45 — Camunda 8 message semantics  (BPMN: 45_messages.bpmn)
 *
 * Tests:
 *   A) A message reaches only the instance whose zeebe:subscription
 *      correlationKey (evaluated from its variables) matches; numbers as keys.
 *   B) A message published with a time to live before anyone waits for it
 *      is correlated when the subscription opens — once per process.
 *   C) A kept message expires: published with a short time to live, it no
 *      longer correlates afterwards.
 *   D) A messageId deduplicates publications while the first is alive.
 *   E) A message start event starts an instance — only one active instance
 *      per correlation key.
 *   F) Event-based gateway: the message wins over the timer, and the timer
 *      wins when no message comes (its losing message no longer correlates).
 *   G) An interrupting message boundary event with a correlation key.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN = path.join(__dirname, 'bpmn', '45_messages.bpmn');
const client = new BpmnGoClient();
const run = Date.now().toString(36);

async function publish(body: Record<string, unknown>) {
  const res = await client.api.post('/v2/messages/publication', body);
  // Camunda 8 answers with the message's key only: what it correlated to
  // shows in the instances.
  return res.data as { messageKey: string; tenantId: string };
}

// activeInstances returns the keys of the active instances of a process.
async function activeInstances(processId: string): Promise<string[]> {
  const res = await client.api.post('/v2/process-instances/search', { filter: { processDefinitionId: processId, state: 'ACTIVE' } });
  return (res.data.items ?? []).map((i: any) => String(i.processInstanceKey));
}

// startedSince returns the instances of processId started since before.
async function startedSince(processId: string, before: string[], expected: number): Promise<string[]> {
  let fresh: string[] = [];
  for (let i = 0; i < 20; i++) {
    fresh = (await activeInstances(processId)).filter(k => !before.includes(k));
    if (fresh.length >= expected && expected > 0) break;
    await sleep(250);
  }
  return fresh;
}

async function start(key: string, vars: Record<string, unknown>) {
  const inst = await client.startProcess(key, vars);
  return (inst.processInstanceId ?? inst.id) as string;
}

async function hasTask(instanceId: string, taskKey: string) {
  const tasks = await client.getTasks(instanceId);
  return tasks.some(t => t.taskDefinitionKey === taskKey);
}

async function finish(instanceId: string, taskKey: string) {
  const task = await client.waitForTask(taskKey, instanceId, 10000);
  await client.completeTask(task.id);
  await client.waitForProcessEnd(instanceId, 10000);
}

async function main() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Messages Test');

  await runSuite({
    name: '45 — Camunda 8 message semantics',
    tests: [
      {
        name: '[A] Messages correlate by correlation key only',
        async fn() {
          const k1 = `A1-${run}`, k2 = `A2-${run}`;
          const i1 = await start('MsgKey45', { orderId: k1 });
          const i2 = await start('MsgKey45', { orderId: k2 });
          await sleep(500);

          const res = await publish({ name: 'Paid45', correlationKey: k2, variables: { paidBy: 'card' } });
          assert(typeof res.messageKey === 'string' && res.messageKey !== '', `a publication answers with its messageKey, got ${JSON.stringify(res)}`);
          await client.waitForTask('mk_after', i2, 10000);
          assert(!(await hasTask(i1, 'mk_after')), 'the instance with the other key must still wait');
          const vars = await client.getProcessVariables(i2) as Record<string, { value?: any }>;
          assert(vars.paidBy?.value === 'card', `message variables should be merged, got ${JSON.stringify(vars.paidBy)}`);

          const none = await publish({ name: 'Paid45', correlationKey: `nobody-${run}` });
          assert(none.messageKey !== undefined, 'a key nobody waits for correlates to nothing (and is not an error)');

          await publish({ name: 'Paid45', correlationKey: k1 });
          await finish(i1, 'mk_after');
          await finish(i2, 'mk_after');

          // A number as correlation key value.
          const n = 450000 + Math.floor(Math.random() * 9999);
          const i3 = await start('MsgKey45', { orderId: n });
          await sleep(500);
          const r3 = await publish({ name: 'Paid45', correlationKey: String(n) });
          assert(r3.messageKey !== undefined, 'published');
          await finish(i3, 'mk_after'); // a numeric key matches "${n}": the instance carries on
        },
      },
      {
        name: '[B] A message with a time to live waits for its subscription, once per process',
        async fn() {
          const k = `B-${run}`;
          await publish({ name: 'Paid45', correlationKey: k, timeToLive: 30000, variables: { early: true } });

          const i1 = await start('MsgKey45', { orderId: k });
          await client.waitForTask('mk_after', i1, 10000);
          const vars = await client.getProcessVariables(i1) as Record<string, { value?: any }>;
          assert(vars.early?.value === true, 'the kept message variables should be merged');

          // Same process again: the message already went to it.
          const i2 = await start('MsgKey45', { orderId: k });
          await sleep(2500);
          assert(!(await hasTask(i2, 'mk_after')), 'a message goes at most once to a process');

          await publish({ name: 'Paid45', correlationKey: k });
          await finish(i1, 'mk_after');
          await finish(i2, 'mk_after');
        },
      },
      {
        name: '[C] A kept message expires',
        async fn() {
          const k = `C-${run}`;
          await publish({ name: 'Paid45', correlationKey: k, timeToLive: 1000 });
          await sleep(2500);
          const i = await start('MsgKey45', { orderId: k });
          await sleep(2500);
          assert(!(await hasTask(i, 'mk_after')), 'an expired message must not correlate');
          await publish({ name: 'Paid45', correlationKey: k });
          await finish(i, 'mk_after');
        },
      },
      {
        name: '[D] A messageId deduplicates publications',
        async fn() {
          const k = `D-${run}`, id = `msg-${run}`;
          await publish({ name: 'Paid45', correlationKey: k, timeToLive: 30000, messageId: id });
          let status = 0;
          try {
            await publish({ name: 'Paid45', correlationKey: k, timeToLive: 30000, messageId: id });
          } catch (e: any) {
            status = e.response?.status;
          }
          assert(status === 409, `the same messageId should be refused with 409, got ${status}`);
          // Let the kept message go somewhere so it can't leak into other tests.
          const i = await start('MsgKey45', { orderId: k });
          await finish(i, 'mk_after');
        },
      },
      {
        name: '[E] A message start event starts one active instance per correlation key',
        async fn() {
          const k = `E-${run}`;
          let before = await activeInstances('MsgStart45');
          await publish({ name: 'Begin45', correlationKey: k, variables: { orderId: k, source: 'message' } });
          const s1 = await startedSince('MsgStart45', before, 1);
          assert(s1.length === 1, `expected one started instance, got ${JSON.stringify(s1)}`);
          const i = s1[0];
          await client.waitForTask('ms_task', i, 10000);
          const vars = await client.getProcessVariables(i) as Record<string, { value?: any }>;
          assert(vars.source?.value === 'message', 'message variables start the instance');

          before = await activeInstances('MsgStart45');
          await publish({ name: 'Begin45', correlationKey: k });
          await sleep(1000);
          const s2 = await startedSince('MsgStart45', before, 0);
          assert(s2.length === 0, `no second instance while the first one with the same key is active, got ${JSON.stringify(s2)}`);

          before = await activeInstances('MsgStart45');
          await publish({ name: 'Begin45', correlationKey: `${k}-other` });
          const other = await startedSince('MsgStart45', before, 1);
          assert(other.length === 1, 'another key starts its own instance');

          await finish(i, 'ms_task');
          before = await activeInstances('MsgStart45');
          await publish({ name: 'Begin45', correlationKey: k });
          const s3 = await startedSince('MsgStart45', before, 1);
          assert(s3.length === 1, 'once the first instance ended, the key can start a new one');
          await finish(s3[0], 'ms_task');
          await finish(other[0], 'ms_task');
        },
      },
      {
        name: '[F] Event-based gateway: message or timer, whichever comes first',
        async fn() {
          const k1 = `F1-${run}`, k2 = `F2-${run}`;
          const byMessage = await start('MsgEbg45', { orderId: k1 });
          const byTimer = await start('MsgEbg45', { orderId: k2 });
          await sleep(500);
          await publish({ name: 'Approve45', correlationKey: k1 });
          await client.waitForTask('eb_approved', byMessage, 10000);

          await client.waitForTask('eb_timed_out', byTimer, 15000);
          // After the timer won, the message branch is gone: a late message
          // changes nothing.
          await publish({ name: 'Approve45', correlationKey: k2 });

          // The message winner's timer was cancelled: it stays on its branch.
          await sleep(3000);
          assert(!(await hasTask(byMessage, 'eb_timed_out')), 'the losing timer must not fire');

          await finish(byMessage, 'eb_approved');
          await finish(byTimer, 'eb_timed_out');
        },
      },
      {
        name: '[G] Interrupting message boundary event with a correlation key',
        async fn() {
          const k = `G-${run}`;
          const i = await start('MsgBoundary45', { orderId: k });
          const other = await start('MsgBoundary45', { orderId: `${k}-other` });
          await client.waitForTask('mb_work', i, 10000);
          await publish({ name: 'Cancel45', correlationKey: k });
          await client.waitForTask('mb_cancelled', i, 10000);
          assert(!(await hasTask(i, 'mb_work')), 'the interrupted task should be gone');
          assert(await hasTask(other, 'mb_work'), 'the other instance keeps working');

          await finish(i, 'mb_cancelled');
          await finish(other, 'mb_work');
        },
      },
    ],
  });
}

main().catch(err => { console.error(err); process.exit(1); });
