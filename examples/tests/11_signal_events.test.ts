/**
 * Suite 11 — Signal Events  (BPMN: 11_inventory_broadcast.bpmn)
 * Source: https://camunda.com/bpmn/reference/ — Signal Events
 *
 * Tests:
 *   A) Single process parked at signal catch event, resumed by broadcast
 *   B) Multiple instances all receive the same broadcast signal
 *   C) Unknown signal returns empty count (no error)
 *
 * A signal is a broadcast: instances left waiting by an earlier run are
 * resumed too, so counts are lower bounds; what is checked exactly is that
 * each of this run's instances is parked before, and resumed after.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN = path.join(__dirname, 'bpmn', '11_inventory_broadcast.bpmn');
const client = new BpmnGoClient();

async function broadcastSignal(name: string, variables: Record<string, unknown> = {}): Promise<any> {
  return (await client.api.post('/engine-rest/signal', { name, variables })).data;
}

async function work(topic: string, instanceId: string) {
  await client.pollExternalTask(topic, async () => ({}), 15000, instanceId);
}

// Waits until every instance in ids has a token on the signal catch event.
async function waitParked(ids: string[]) {
  const deadline = Date.now() + 10000;
  for (;;) {
    const positions = (await client.api.get('/engine-rest/process-definition/key/inventoryAlert/instance-positions')).data as any[];
    const parked = new Set(positions.filter(p => p.elementId === 'waitForRestock').map(p => p.processInstanceId));
    if (ids.every(id => parked.has(id))) return;
    if (Date.now() > deadline) throw new Error(`not all parked at waitForRestock: ${ids.filter(id => !parked.has(id))}`);
    await sleep(300);
  }
}

async function run() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Inventory Broadcast Test');

  await runSuite({
    name: '11 — Signal Events',
    tests: [

      // ── A: single instance receives signal ────────────────────────────────
      {
        name: '[A] Process parked at signal catch, resumed by RestockAlert broadcast',
        async fn() {
          const inst = await client.startProcess('inventoryAlert', { product: 'widget-A' });
          const instanceId = inst.processInstanceId ?? inst.id;

          await work('check-inventory', instanceId);
          await waitParked([instanceId]);

          const result = await broadcastSignal('RestockAlert', { restockedProduct: 'widget-A' });
          assert(result.count >= 1, `expected count >= 1, got ${result.count}`);

          await work('update-inventory', instanceId);
          await client.waitForProcessEnd(instanceId, 10000);
          const vars = await client.getProcessVariables(instanceId) as Record<string, any>;
          const restocked = vars.restockedProduct?.value ?? vars.restockedProduct;
          assert(restocked === 'widget-A', `signal variables must reach the instance, got ${JSON.stringify(vars.restockedProduct)}`);
        },
      },

      // ── B: broadcast reaches multiple instances ───────────────────────────
      {
        name: '[B] Broadcast signal resumes ALL waiting instances',
        async fn() {
          const ids: string[] = [];
          for (let i = 0; i < 3; i++) {
            const inst = await client.startProcess('inventoryAlert', { product: `widget-B${i}` });
            ids.push(inst.processInstanceId ?? inst.id);
          }
          for (const id of ids) await work('check-inventory', id);
          await waitParked(ids);

          // Broadcast once — all 3 should be resumed
          const result = await broadcastSignal('RestockAlert');
          assert(result.count >= 3, `expected count >= 3, got ${result.count}`);

          for (const id of ids) {
            await work('update-inventory', id);
            await client.waitForProcessEnd(id, 10000);
          }
        },
      },

      // ── C: unknown signal returns 0 count (no error) ─────────────────────
      {
        name: '[C] Broadcasting unknown signal returns count=0 without error',
        async fn() {
          const result = await broadcastSignal('NonExistentSignal_' + Date.now());
          assert(result.count === 0, `expected count=0, got ${result.count}`);
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
