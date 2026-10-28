/**
 * Suite 02 — Exclusive Gateway (XOR)  (BPMN: 02_recipe_selection.bpmn)
 * Source: https://camunda.com/bpmn/reference/ — Exclusive Gateway example
 *
 * Scenario: Christian is hungry. He picks a recipe (pasta, steak, or salad).
 *   • pasta / steak → external worker cooks the meal → user task serves it
 *   • salad         → user task prepares it directly
 *
 * Tests:
 *   A) Hot-meal path (recipe=pasta) — XOR routes to cook_meal service task
 *   B) Salad path   (recipe=salad)  — XOR routes to prepare_salad user task
 *   C) External task failure with retry
 *   D) Variables set by external worker are visible on subsequent tasks
 *      (checked in A)
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN = path.join(__dirname, 'bpmn', '02_recipe_selection.bpmn');
const client = new BpmnGoClient();

async function run() {
  await client.loginAsSuperUser();
  await client.deployBpmn(BPMN, 'Recipe Selection Test');

  await runSuite({
    name: '02 — Exclusive Gateway (Recipe Selection)',
    tests: [

      // ── A: hot-meal path ────────────────────────────────────────────────────
      {
        name: '[Pasta path] XOR routes to cook_meal → serve_hot_meal → Meal Served',
        async fn() {
          const inst = await client.startProcess('RecipeSelectionProcess', {
            chef: 'christian',
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          // Step 1: Christian chooses his recipe
          const chooseTask = await client.waitForTask('choose_recipe', instanceId, 10000);
          assert(chooseTask.assignee === 'christian', 'wrong assignee on choose_recipe');
          await client.completeTask(chooseTask.id, { recipe: 'pasta' });

          // Step 2: External worker cooks the pasta
          await client.pollExternalTask('cook_meal', async task => ({
            mealReady: true,
            cookedAt: new Date().toISOString(),
            dish: task.variables?.recipe?.value ?? 'unknown',
          }), 15000, instanceId);

          // Step 3: Serve the hot meal
          const serveTask = await client.waitForTask('serve_hot_meal', instanceId, 10000);
          assert(serveTask.assignee === 'christian', 'wrong assignee on serve_hot_meal');

          // D) What the worker wrote is visible from the next task on.
          const vars = await client.getProcessVariables(instanceId) as Record<string, any>;
          const val = (v: any) => (v && typeof v === 'object' && 'value' in v ? v.value : v);
          assert(val(vars.dish) === 'pasta' && val(vars.mealReady) === true,
            `worker variables must reach the process: dish=${JSON.stringify(vars.dish)} mealReady=${JSON.stringify(vars.mealReady)}`);
          await client.completeTask(serveTask.id);

          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── B: salad path ───────────────────────────────────────────────────────
      {
        name: '[Salad path] XOR routes to prepare_salad user task → Salad Ready',
        async fn() {
          const inst = await client.startProcess('RecipeSelectionProcess', {
            chef: 'christian',
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          const chooseTask = await client.waitForTask('choose_recipe', instanceId, 10000);
          await client.completeTask(chooseTask.id, { recipe: 'salad' });

          // No cook_meal external task — process goes directly to prepare_salad
          const saladTask = await client.waitForTask('prepare_salad', instanceId, 10000);
          assert(saladTask.candidateGroup === 'kitchen', `expected candidateGroup "kitchen", got "${saladTask.candidateGroup}"`);

          // Anyone in kitchen can claim it
          await client.claimTask(saladTask.id, 'sous_chef');
          await client.completeTask(saladTask.id, { saladDone: true });

          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── C: steak path ───────────────────────────────────────────────────────
      {
        name: '[Steak path] XOR also routes recipe=steak to cook_meal service task',
        async fn() {
          const inst = await client.startProcess('RecipeSelectionProcess', {
            chef: 'christian',
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          const chooseTask = await client.waitForTask('choose_recipe', instanceId, 10000);
          await client.completeTask(chooseTask.id, { recipe: 'steak' });

          await client.pollExternalTask('cook_meal', async () => ({
            mealReady: true,
            dish: 'steak',
          }), 15000, instanceId);

          const serveTask = await client.waitForTask('serve_hot_meal', instanceId, 10000);
          await client.completeTask(serveTask.id);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── D: external task failure + retry ────────────────────────────────────
      {
        name: '[Failure/Retry] cook_meal worker fails once, succeeds on retry',
        async fn() {
          const inst = await client.startProcess('RecipeSelectionProcess', {
            chef: 'christian',
          });
          const instanceId = inst.processInstanceId ?? inst.id;

          const chooseTask = await client.waitForTask('choose_recipe', instanceId, 10000);
          await client.completeTask(chooseTask.id, { recipe: 'pasta' });

          // Fetches this instance's cook_meal job, if available now; others
          // are released at once.
          const fetchOurs = async () => {
            let ours: any;
            for (const t of await client.fetchAndLock([{ name: 'cook_meal' }])) {
              if (!ours && t.processInstanceId === instanceId) ours = t;
              else await client.api.post(`/engine-rest/external-task/${t.id}/unlock`, {});
            }
            return ours;
          };

          // First fetch: fail it with retries=1 (what remains, Camunda 7).
          let first: any;
          const deadline = Date.now() + 10000;
          while (!first && Date.now() < deadline) {
            first = await fetchOurs();
            if (!first) await sleep(300);
          }
          assert(!!first, 'Expected to fetch cook_meal task for failure');
          await client.failExternalTask(first.id, 'Temporary cooking failure', 1);
          const failedAt = Date.now();

          // Not available again before the 5 s retryTimeout...
          assert(!(await fetchOurs()), 'the job must not be fetchable right after the failure');
          await sleep(2000);
          assert(!(await fetchOurs()), 'the job must not be fetchable before its retryTimeout');

          // ...then the same job comes back.
          let retry: any;
          while (!retry && Date.now() - failedAt < 15000) {
            retry = await fetchOurs();
            if (!retry) await sleep(300);
          }
          assert(!!retry, 'the job must come back after its retryTimeout');
          assert(retry.id === first.id, `the same job must come back, got ${retry.id} after ${first.id}`);
          assert(Date.now() - failedAt >= 4500, `came back after ${Date.now() - failedAt} ms, before the 5 s retryTimeout`);
          await client.completeExternalTask(retry.id, { mealReady: true, retriedAt: new Date().toISOString() });

          const serveTask = await client.waitForTask('serve_hot_meal', instanceId, 10000);
          await client.completeTask(serveTask.id);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
