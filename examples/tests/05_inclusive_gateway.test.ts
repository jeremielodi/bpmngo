/**
 * Suite 05 — Inclusive Gateway (OR)  (BPMN: 05_fitness_planner.bpmn)
 * Source: https://camunda.com/bpmn/reference/ — Inclusive Gateway example
 *
 * Scenario: A customer selects fitness activities (running, swimming, cycling).
 * The inclusive gateway activates only the selected branches. All active
 * branches must complete before the plan is confirmed.
 *
 * Tests:
 *   A) All 3 branches active  — wantRunning=true, wantSwimming=true, wantCycling=true
 *   B) Single branch active   — wantRunning=true only
 *   C) Two branches active    — wantRunning=true, wantSwimming=true
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN = path.join(__dirname, 'bpmn', '05_fitness_planner.bpmn');
const client = new BpmnGoClient();

// Job types created for the instance, once `expected` all exist plus a
// moment more, so a branch created late is still seen. Checked while the
// instance runs: finished jobs are archived, and the activity history does
// not list the service tasks of gateway branches.
async function jobTypesOnceCreated(instanceId: string, expected: string[]): Promise<Set<string>> {
  const types = async () =>
    new Set(((await client.api.get('/jobs', { params: { processInstanceId: instanceId } })).data as any[] ?? []).map(j => j.jobType));
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const seen = await types();
    if (expected.every(t => seen.has(t))) break;
    await sleep(300);
  }
  await sleep(1000);
  const seen = await types();
  assert(expected.every(t => seen.has(t)), `expected jobs ${expected}, got ${[...seen]}`);
  return seen;
}

async function run() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Fitness Planner Test');

  await runSuite({
    name: '05 — Inclusive Gateway / OR (Fitness Planner)',
    tests: [

      // ── A: all 3 branches ────────────────────────────────────────────────────
      {
        name: '[All branches] All 3 activities selected — 3 service tasks created and all must complete',
        async fn() {
          const inst = await client.startProcess('FitnessPlannerProcess', {
            wantRunning: true,
            wantSwimming: true,
            wantCycling: true,
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          // All 3 branches run concurrently — poll them in parallel
          await Promise.all([
            client.pollExternalTask('register_running',  async () => ({ registered: true }), 15000, instanceId),
            client.pollExternalTask('register_swimming', async () => ({ registered: true }), 15000, instanceId),
            client.pollExternalTask('register_cycling',  async () => ({ registered: true }), 15000, instanceId),
          ]);

          await client.waitForProcessEnd(instanceId, 10000);
          const final = await client.getProcessInstance(instanceId);
          assert(!final || final.status === 'completed', `expected completed, got ${final?.status}`);
        },
      },

      // ── B: single branch ─────────────────────────────────────────────────────
      {
        name: '[Single branch] Only running selected — only register_running job created',
        async fn() {
          const inst = await client.startProcess('FitnessPlannerProcess', {
            wantRunning: true,
            wantSwimming: false,
            wantCycling: false,
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          // The point of an inclusive gateway: unselected branches never run.
          const types = await jobTypesOnceCreated(instanceId, ['register_running']);
          assert(!types.has('register_swimming') && !types.has('register_cycling'),
            `unselected branches must not run, jobs: ${[...types]}`);

          await client.pollExternalTask('register_running', async () => ({ registered: true }), 15000, instanceId);

          await client.waitForProcessEnd(instanceId, 10000);
          const final = await client.getProcessInstance(instanceId);
          assert(!final || final.status === 'completed', `expected completed, got ${final?.status}`);
        },
      },

      // ── C: two branches ──────────────────────────────────────────────────────
      {
        name: '[Two branches] Running + swimming selected — both complete before join fires',
        async fn() {
          const inst = await client.startProcess('FitnessPlannerProcess', {
            wantRunning: true,
            wantSwimming: true,
            wantCycling: false,
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          const types = await jobTypesOnceCreated(instanceId, ['register_running', 'register_swimming']);
          assert(!types.has('register_cycling'), `cycling was not selected and must not run, jobs: ${[...types]}`);

          // Complete running alone: the join must keep waiting for swimming.
          await client.pollExternalTask('register_running', async () => ({ registered: true }), 15000, instanceId);
          await sleep(1000);
          const midway = await client.getProcessInstance(instanceId);
          assert(!!midway && midway.status !== 'completed', `the join fired before swimming completed: ${midway?.status}`);

          await client.pollExternalTask('register_swimming', async () => ({ registered: true }), 15000, instanceId);

          await client.waitForProcessEnd(instanceId, 10000);
          const final = await client.getProcessInstance(instanceId);
          assert(!final || final.status === 'completed', `expected completed, got ${final?.status}`);
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
