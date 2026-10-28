/**
 * Suite 08 — Process Definition Versioning
 * Source: https://camunda.com/bpmn/reference/ — Process Versioning
 *
 * Scenario: Deploy the same BPMN process key multiple times. Each deploy
 *           creates a new version. Instances started before the new deployment
 *           keep running on their original version; new instances use the latest.
 *           Users can also start a specific version by definition ID.
 *
 * Tests:
 *   A) Two deploys of the same key → version 1 and version 2 created
 *   B) List all versions for a key → both visible via /engine-rest/process-definition?key=X
 *   C) Start by key → latest version (v2) is used
 *   D) Start by definition ID → specific version is used
 *   E) Instance from v1 retains its original process_definition_id (not v2)
 */
import * as path from 'path';
import * as fs from 'fs';
import FormData from 'form-data';
import { BpmnGoClient, runSuite, assert } from './client';

const runId = Date.now().toString(36);

// Posts files to a deployment route; returns the raw response.
async function deployFiles(route: string, files: Array<{ name: string; content: Buffer | string }>, name = `deploy-${runId}`) {
  const form = new FormData();
  for (const f of files) form.append('resources', Buffer.from(f.content), { filename: f.name });
  form.append('deployment-name', name);
  return client.api.post(route, form, { headers: form.getHeaders(), validateStatus: () => true });
}

// We reuse the simple support ticket BPMN (SupportTicketProcess) for versioning tests —
// we just deploy it twice to produce v1 and v2 of the same process key.
const BPMN = path.join(__dirname, 'bpmn', '07_support_ticket.bpmn');
const client = new BpmnGoClient();

async function run() {
  await client.loginAsSuperUser();

  let v1Id: string;
  let v2Id: string;
  const KEY = 'SupportTicketProcess';

  await runSuite({
    name: '08 — Process Definition Versioning',
    tests: [

      // ── A: deploy twice → two versions ───────────────────────────────────────
      {
        name: '[Deploy] Two deploys of the same key produce version 1 and version 2',
        async fn() {
          await client.deployBpmn(BPMN, 'SupportTicket v1 deploy');
          await client.deployBpmn(BPMN, 'SupportTicket v2 deploy');

          // List all versions
          const res = await client.api.get(`/engine-rest/process-definition?key=${KEY}`);
          const defs = res.data as any[];
          assert(defs.length >= 2, `expected at least 2 versions, got ${defs.length}`);

          const versions = defs.map(d => d.version).sort((a, b) => a - b);
          const v1 = defs.find(d => d.version === versions[versions.length - 2]); // second-to-latest
          const v2 = defs.find(d => d.version === versions[versions.length - 1]); // latest
          assert(v1 !== undefined, 'v1 not found');
          assert(v2 !== undefined, 'v2 not found');
          assert(v2.version > v1.version, `v2 version (${v2.version}) should be > v1 (${v1.version})`);

          v1Id = v1.id;
          v2Id = v2.id;
        },
      },

      // ── B: list versions ──────────────────────────────────────────────────────
      {
        name: '[List] GET /engine-rest/process-definition?key returns all versions, latestVersion=true returns one',
        async fn() {
          assert(v1Id !== undefined, 'run test A first');

          const allRes = await client.api.get(`/engine-rest/process-definition?key=${KEY}`);
          const allDefs = allRes.data as any[];
          assert(allDefs.length >= 2, `expected >=2, got ${allDefs.length}`);

          const latestRes = await client.api.get(`/engine-rest/process-definition?key=${KEY}&latestVersion=true`);
          const latestDefs = latestRes.data as any[];
          assert(latestDefs.length === 1, `expected exactly 1 latest, got ${latestDefs.length}`);
          assert(latestDefs[0].id === v2Id, `latest should be v2 (${v2Id}), got ${latestDefs[0].id}`);
        },
      },

      // ── C: start by key → latest version ─────────────────────────────────────
      {
        name: '[Start by key] Starting by process key always uses the latest version',
        async fn() {
          assert(v2Id !== undefined, 'run test A first');

          const inst = await client.startProcess(KEY, { ticketId: 'TKT-VER-001' });
          const instanceId = inst.processInstanceId ?? inst.id;

          // Resolve the job so the process can finish
          await client.pollExternalTask('classify_ticket', async () => ({ category: 'test' }), 10000, instanceId);
          await client.waitForProcessEnd(instanceId, 10000);

          // Verify the instance used the latest definition
          const piRes = await client.api.get(`/engine-rest/process-instance/${instanceId}`);
          const pi = piRes.data as any;
          assert(
            pi.processDefinitionId === v2Id,
            `expected processDefinitionId=${v2Id}, got ${pi.processDefinitionId}`
          );
        },
      },

      // ── D: start by definition ID → specific version ──────────────────────────
      {
        name: '[Start by ID] POST /engine-rest/process-definition/:id/start uses that exact version',
        async fn() {
          assert(v1Id !== undefined, 'run test A first');

          const res = await client.api.post(`/engine-rest/process-definition/${v1Id}/start`, {
            variables: { ticketId: 'TKT-VER-V1', source: 'versioning-test' },
          });
          const instanceId = res.data.processInstanceId;
          assert(res.data.definitionId === v1Id, `expected definitionId=${v1Id}, got ${res.data.definitionId}`);
          assert(typeof res.data.version === 'number', 'version field should be a number');

          // Drain the job
          await client.pollExternalTask('classify_ticket', async () => ({ done: true }), 10000, instanceId);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── E: instance retains its original definition ID ────────────────────────
      {
        name: '[Isolation] Instance started from v1 still reports v1 processDefinitionId after v2 deployed',
        async fn() {
          assert(v1Id !== undefined, 'run test A first');

          // Start a fresh instance locked to v1
          const res = await client.api.post(`/engine-rest/process-definition/${v1Id}/start`, {
            variables: { ticketId: 'TKT-VER-ISO' },
          });
          const instanceId = res.data.processInstanceId;

          // Deploy v3 of the same process (simulates further evolution)
          await client.deployBpmn(BPMN, 'SupportTicket v3 deploy');

          // The v1 instance should still show the v1 definition ID
          const piRes = await client.api.get(`/engine-rest/process-instance/${instanceId}`);
          const pi = piRes.data as any;
          assert(
            pi.processDefinitionId === v1Id,
            `instance should still reference v1 (${v1Id}), got ${pi.processDefinitionId}`
          );

          // Clean up — complete the job
          await client.pollExternalTask('classify_ticket', async () => ({}), 10000, instanceId);
          await client.waitForProcessEnd(instanceId, 10000);
        },
      },

      // ── F/G/H: deployment routes, invalid files, positions ────────────────
      {
        name: '[F] Every deployment route deploys BPMN + DMN + form alike, one version each',
        async fn() {
          const key = `Routes08_${runId}`;
          const bpmn = fs.readFileSync(path.join(__dirname, 'bpmn', '08_positions.bpmn'), 'utf8').replace(/Positions08/g, key);
          const dmn = fs.readFileSync(path.join(__dirname, 'bpmn', '17_approval_decision.dmn'));
          const form = fs.readFileSync(path.join(__dirname, 'bpmn', '18_review_form.form'));
          const routes = ['/engine-rest/deployment/create', '/engine-rest/v2/deployment/create', '/engine-rest/v2/deployments', '/v2/resources/deployments'];
          for (const [i, route] of routes.entries()) {
            const res = await deployFiles(route, [
              { name: `${key}.bpmn`, content: bpmn },
              { name: 'decision.dmn', content: dmn },
              { name: 'review.form', content: form },
            ]);
            assert(res.status < 300, `${route}: ${res.status} ${JSON.stringify(res.data)}`);
            const defs = (await client.api.get(`/engine-rest/process-definition?key=${key}`)).data as any[];
            assert(defs.length === i + 1, `${route}: expected ${i + 1} versions of ${key}, got ${defs.length}`);
          }
        },
      },
      {
        name: '[G] Invalid files are a 400; a non-executable process is not deployed',
        async fn() {
          const bad = await deployFiles('/engine-rest/deployment/create', [{ name: 'bad.bpmn', content: '<definitions><process id="x"><startEvent' }]);
          assert(bad.status === 400, `truncated BPMN: expected 400, got ${bad.status} ${JSON.stringify(bad.data)}`);
          const empty = await deployFiles('/engine-rest/deployment/create', [{ name: 'empty.bpmn', content: '<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"/>' }]);
          assert(empty.status === 400, `BPMN without process: expected 400, got ${empty.status}`);
          const noExec = await deployFiles('/engine-rest/deployment/create', [{ name: 'noexec.bpmn',
            content: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="NoExec08_${runId}" isExecutable="false"><startEvent id="s"/></process></definitions>` }]);
          assert(noExec.status === 400, `only non-executable processes: expected 400, got ${noExec.status}`);

          const key = `Exec08_${runId}`;
          const mixed = fs.readFileSync(path.join(__dirname, 'bpmn', '08_positions.bpmn'), 'utf8')
            .replace(/Positions08/g, key).replace(/Customer08/g, `Customer08_${runId}`);
          const ok = await deployFiles('/engine-rest/deployment/create', [{ name: 'mixed.bpmn', content: mixed }]);
          assert(ok.status < 300, `mixed file: ${ok.status}`);
          assert((await client.api.get(`/engine-rest/process-definition?key=${key}`)).data.length === 1, 'the executable process is deployed');
          assert((await client.api.get(`/engine-rest/process-definition?key=Customer08_${runId}`)).data.length === 0, 'the non-executable one is not');
        },
      },
      {
        name: '[H] Definition by id; token counts and instance positions follow the instances',
        async fn() {
          const key = `Positions08_${runId}`;
          const bpmn = fs.readFileSync(path.join(__dirname, 'bpmn', '08_positions.bpmn'), 'utf8').replace(/Positions08/g, key).replace(/Customer08/g, `C08_${runId}`);
          await deployFiles('/engine-rest/deployment/create', [{ name: `${key}.bpmn`, content: bpmn }]);
          const def = (await client.api.get(`/engine-rest/process-definition/key/${key}`)).data;
          const byId = await client.api.get(`/engine-rest/process-definition/${def.id}`, { validateStatus: () => true });
          assert(byId.status === 200 && byId.data.key === key && byId.data.id === def.id, `GET by id: ${JSON.stringify(byId.data)}`);
          const unknown = await client.api.get('/engine-rest/process-definition/00000000-0000-0000-0000-000000000000', { validateStatus: () => true });
          assert(unknown.status === 404, `unknown definition id: expected 404, got ${unknown.status}`);

          const instId = (i: any) => (i.processInstanceId ?? i.id) as string;
          const a = instId(await client.startProcess(key, {}));
          const b = instId(await client.startProcess(key, {}));
          const first = await client.waitForTask('p_first', b, 10000);
          await client.completeTask(first.id);
          await client.waitForTask('p_second', b, 10000);

          const counts = async () => Object.fromEntries(((await client.api.get(`/engine-rest/process-definition/key/${key}/token-counts`)).data as any[]).map(c => [c.elementId, c.count]));
          const positions = async () => ((await client.api.get(`/engine-rest/process-definition/key/${key}/instance-positions`)).data as any[]);
          const both = await counts();
          assert(both.p_first === 1 && both.p_second === 1 && Object.keys(both).length === 2, `token counts: ${JSON.stringify(both)}`);
          const pos = await positions();
          assert(pos.some(p => p.processInstanceId === a && p.elementId === 'p_first') && pos.some(p => p.processInstanceId === b && p.elementId === 'p_second'),
            `instance positions: ${JSON.stringify(pos)}`);

          // A finished instance no longer counts.
          await client.completeTask((await client.waitForTask('p_second', b, 10000)).id);
          await client.waitForProcessEnd(b, 10000);
          assert(JSON.stringify(await counts()) === JSON.stringify({ p_first: 1 }), `after b ends: ${JSON.stringify(await counts())}`);
          await client.api.delete(`/engine-rest/process-instance/${a}`);
          assert(JSON.stringify(await counts()) === '{}', `after a is cancelled: ${JSON.stringify(await counts())}`);
          assert((await positions()).length === 0, 'no positions left');

          // Restricted to someone else: 404, as the definition itself.
          const email = `pos08-${runId}@goflow.com`;
          await client.api.post('/users', { email, full_name: email, password: 'password123' });
          const other = await client.api.post('/users', { email: `pos08b-${runId}@goflow.com`, full_name: 'x', password: 'password123' });
          await client.api.post(`/engine-rest/process-definition/key/${key}/permissions`, { granteeType: 'user', granteeId: other.data.user.id, permission: 'VIEW' });
          const outsider = new BpmnGoClient();
          await outsider.login(email, 'password123');
          for (const what of ['token-counts', 'instance-positions']) {
            const r = await outsider.api.get(`/engine-rest/process-definition/key/${key}/${what}`, { validateStatus: () => true });
            assert(r.status === 404, `${what} without a grant: expected 404, got ${r.status}`);
          }
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
