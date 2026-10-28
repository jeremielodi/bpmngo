/**
 * Suite 12 — Event-based Gateway  (BPMN: 12_customer_decision.bpmn)
 * Source: https://camunda.com/bpmn/reference/ — Event-Based Gateway
 *
 * Tests:
 *   A) CustomerApproved message fires first → order is processed
 *   B) CustomerRejected message fires first → quote is archived
 *   C) After first event fires, second event has no effect (cancelled subscription)
 *
 * Every message names its instance (processInstanceId) and every job is
 * taken from its own instance, so instances left over by an earlier run
 * cannot interfere.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN = path.join(__dirname, 'bpmn', '12_customer_decision.bpmn');
const client = new BpmnGoClient();
const ok = { validateStatus: () => true };

// Correlates messageName to instanceId; with waitForSubscription, retries
// while the gateway has not opened its subscriptions yet.
async function correlateMessage(messageName: string, instanceId: string, waitForSubscription = true) {
  const deadline = Date.now() + 10000;
  for (;;) {
    const r = await client.api.post('/engine-rest/message', { messageName, processInstanceId: instanceId }, ok);
    if (r.status < 300 || !waitForSubscription || Date.now() > deadline) return r;
    await sleep(300);
  }
}

// Completes this instance's job of the given topic; fails if none appears.
async function completeExternalTask(topic: string, instanceId: string): Promise<void> {
  await client.pollExternalTask(topic, async () => ({}), 10000, instanceId);
}

// The job types this instance has had, read while it runs.
async function jobTypes(instanceId: string): Promise<string[]> {
  return ((await client.api.get('/jobs', { params: { processInstanceId: instanceId } })).data ?? []).map((j: any) => j.jobType);
}

async function run() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Customer Decision Test');

  await runSuite({
    name: '12 — Event-based Gateway',
    tests: [

      // ── A: approval path ──────────────────────────────────────────────────
      {
        name: '[A] CustomerApproved message → process-order branch taken',
        async fn() {
          const inst = await client.startProcess('customerDecision', { quoteId: 'Q-001' });
          const instanceId = inst.processInstanceId ?? inst.id;

          await completeExternalTask('send-quote', instanceId);
          const r = await correlateMessage('CustomerApproved', instanceId);
          assert(r.status < 300, `CustomerApproved message failed: ${r.status}`);

          await completeExternalTask('process-order', instanceId);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── B: rejection path ────────────────────────────────────────────────
      {
        name: '[B] CustomerRejected message → archive-quote branch taken',
        async fn() {
          const inst = await client.startProcess('customerDecision', { quoteId: 'Q-002' });
          const instanceId = inst.processInstanceId ?? inst.id;

          await completeExternalTask('send-quote', instanceId);
          const r = await correlateMessage('CustomerRejected', instanceId);
          assert(r.status < 300, `CustomerRejected message failed: ${r.status}`);

          await completeExternalTask('archive-quote', instanceId);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── C: second message after first is a no-op (subscription cancelled) ─
      {
        name: '[C] After approval fires, sending rejection has no effect on the instance',
        async fn() {
          const inst = await client.startProcess('customerDecision', { quoteId: 'Q-003' });
          const instanceId = inst.processInstanceId ?? inst.id;

          await completeExternalTask('send-quote', instanceId);
          const r1 = await correlateMessage('CustomerApproved', instanceId);
          assert(r1.status < 300, `CustomerApproved failed: ${r1.status}`);

          // The gateway closed its other subscription: rejection no longer
          // correlates to THIS instance...
          const r2 = await correlateMessage('CustomerRejected', instanceId, false);
          assert(r2.status >= 400, `CustomerRejected still correlated to the instance (HTTP ${r2.status})`);

          // ...and the archive-quote branch never starts.
          await sleep(1000);
          const types = await jobTypes(instanceId);
          assert(types.includes('process-order'), `process-order expected, jobs: ${types}`);
          assert(!types.includes('archive-quote'), `archive-quote must never be created, jobs: ${types}`);

          await completeExternalTask('process-order', instanceId);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
