/**
 * Suite 13 — Call Activity  (BPMN: 13_call_activity_main.bpmn + 13_credit_check_process.bpmn)
 * Source: https://camunda.com/bpmn/reference/ — Call Activity
 *
 * Tests:
 *   A) Main process invokes sub-process via Call Activity, waits for it, then continues
 *   B) Exactly one sub-process instance is created per call, and it ends
 *   C) After sub-process completes, main process resumes and completes
 *
 * Each job is taken from its own instance (the main one, or the child found
 * through history), so instances left by an earlier run cannot interfere.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert, sleep } from './client';

const BPMN_MAIN = path.join(__dirname, 'bpmn', '13_call_activity_main.bpmn');
const BPMN_SUB = path.join(__dirname, 'bpmn', '13_credit_check_process.bpmn');
const client = new BpmnGoClient();

async function work(topic: string, instanceId: string) {
  await client.pollExternalTask(topic, async () => ({}), 15000, instanceId);
}

// creditCheckProcess instances started since `since` (ISO time).
async function childrenSince(since: string): Promise<any[]> {
  return (await client.api.get('/engine-rest/history/process-instance', {
    params: { processDefinitionKey: 'creditCheckProcess', startedAfter: since },
  })).data as any[];
}

// Starts a loan application and runs it up to its call activity; returns
// the main instance and the one child it started.
async function startUpToChild(applicantId: string) {
  const inst = await client.startProcess('loanApplication', { applicantId });
  const mainId = (inst.processInstanceId ?? inst.id) as string;
  // The server's clock, not this host's: children started after the main
  // instance are the ones it called (tests run one at a time).
  const mains = (await client.api.get('/engine-rest/history/process-instance', { params: { processDefinitionKey: 'loanApplication' } })).data as any[];
  const since = mains.find(m => m.id === mainId)?.startTime as string;
  assert(!!since, 'main instance not found in history');
  await work('prepare-documents', mainId);

  let children: any[] = [];
  const deadline = Date.now() + 10000;
  while (children.length === 0 && Date.now() < deadline) {
    children = await childrenSince(since);
    if (children.length === 0) await sleep(300);
  }
  assert(children.length === 1, `one creditCheckProcess instance expected, got ${children.length}`);
  return { mainId, childId: children[0].id as string, since };
}

async function runChild(childId: string) {
  await work('retrieve-credit-score', childId);
  await work('evaluate-risk', childId);
}

async function run() {
  await client.loginAsSuperUser();

  // Deploy both processes
  await client.deployBpmn(BPMN_SUB, 'Credit Check Sub-Process');
  await client.deployBpmn(BPMN_MAIN, 'Loan Application Main');

  await runSuite({
    name: '13 — Call Activity',
    tests: [

      // ── A: end-to-end call activity flow ──────────────────────────────────
      {
        name: '[A] Main process invokes sub-process and completes after sub ends',
        async fn() {
          const { mainId, childId } = await startUpToChild('APP-001');
          await runChild(childId);
          // Sub-process ends → parent resumes → make-decision runs
          await work('make-decision', mainId);
          await client.waitForProcessEnd(mainId, 15000);
        },
      },

      // ── B: one child per call, and it ends ────────────────────────────────
      {
        name: '[B] Exactly one child process instance is created, and it ends',
        async fn() {
          const { mainId, childId, since } = await startUpToChild('APP-002');
          await runChild(childId);
          await work('make-decision', mainId);
          await client.waitForProcessEnd(mainId, 15000);

          const children = await childrenSince(since);
          assert(children.length === 1, `still one child after the run, got ${children.length}`);
          assert(children[0].endTime !== null && children[0].state !== 'ACTIVE', `the child must be over: ${JSON.stringify(children[0])}`);
        },
      },

      // ── C: main process only completes after sub-process completes ────────
      {
        name: '[C] Main process stays running while sub-process is active',
        async fn() {
          const { mainId, childId } = await startUpToChild('APP-003');

          // Main process should still be running (waiting on call activity)
          const mid = await client.getProcessInstance(mainId) as any;
          assert(mid?.status === 'running', `expected main to be running (waiting), got ${mid?.status}`);

          await runChild(childId);
          await work('make-decision', mainId);
          await client.waitForProcessEnd(mainId, 15000);
          const final = await client.getProcessInstance(mainId);
          assert(!final || final.status === 'completed', `expected completed, got ${final?.status}`);
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
