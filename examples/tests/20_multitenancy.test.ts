/**
 * Suite 20 — Multi-tenancy (Camunda 8 Phase 7)
 *
 * Verifies real tenant isolation: two tenants deploy the SAME process key
 * ("TenantProcess") independently, then start their own instances. A
 * tenant-scoped user must never see, list, search, or directly fetch by ID
 * another tenant's instances or process definitions — while a superuser
 * (no tenant) sees everything, matching existing superuser semantics.
 *
 * Because process definition versions aren't tenant-partitioned (two
 * tenants sharing a process key produce v1/v2 of "the same" key), the
 * running instance's tenant is always stamped from the CALLER's own
 * tenant at start time — not from whichever definition version happens to
 * resolve as "latest". This suite's core assertion is that this holds even
 * under that version ambiguity.
 *
 * Tests:
 *   A) Two tenants deploy the same process key under their own tenant.
 *   B) Each tenant starts an instance; GET/POST list+search endpoints only
 *      return the caller's own tenant's instances.
 *   C) Directly fetching another tenant's instance/process-definition by ID
 *      returns 404/null, not the resource.
 *   D) A superuser (no tenant) sees instances/definitions from BOTH tenants.
 */
import * as path from 'path';
import { BpmnGoClient, runSuite, assert } from './client';

const BPMN = path.join(__dirname, 'bpmn', '20_tenant_process.bpmn');
const superuser = new BpmnGoClient();

async function createTenantUser(email: string, tenantId: string): Promise<BpmnGoClient> {
  // 409 on a re-run against the same database: the user is already there.
  await superuser.api.post('/users', {
    email,
    full_name: email,
    password: 'password123',
    tenant_id: tenantId,
  }, { validateStatus: s => s < 300 || s === 409 });
  const client = new BpmnGoClient();
  await client.login(email, 'password123');
  return client;
}

async function run() {
  await superuser.loginAsSuperUser();

  const userA = await createTenantUser('tenant-a-user@goflow.com', 'tenant-a');
  const userB = await createTenantUser('tenant-b-user@goflow.com', 'tenant-b');

  await runSuite({
    name: '20 — Multi-tenancy',
    tests: [

      // ── A: same process key deployed independently per tenant ─────────────
      {
        name: '[A] Two tenants deploy the same process key under their own tenant',
        async fn() {
          const fs = require('fs');
          const FormData = require('form-data');

          const formA = new FormData();
          formA.append('resources', fs.createReadStream(BPMN));
          formA.append('deployment-name', 'Tenant A Deployment');
          formA.append('tenant-id', 'tenant-a');
          const resA = await superuser.api.post('/engine-rest/deployment/create', formA, { headers: formA.getHeaders() });
          assert(resA.data.deployedProcessDefinitions?.TenantProcess !== undefined, 'expected TenantProcess deployed for tenant-a');

          const formB = new FormData();
          formB.append('resources', fs.createReadStream(BPMN));
          formB.append('deployment-name', 'Tenant B Deployment');
          formB.append('tenant-id', 'tenant-b');
          const resB = await superuser.api.post('/engine-rest/deployment/create', formB, { headers: formB.getHeaders() });
          assert(resB.data.deployedProcessDefinitions?.TenantProcess !== undefined, 'expected TenantProcess deployed for tenant-b');

          // A new process is its deployer's alone: let both tenant users in.
          // Permissions are per key; tenants still isolate the instances.
          for (const user of [userA, userB]) {
            const me = (await user.api.get('/users/me')).data.user;
            for (const permission of ['VIEW', 'START']) {
              await superuser.api.post('/engine-rest/process-definition/key/TenantProcess/permissions', { granteeType: 'user', granteeId: me.id, permission });
            }
          }
        },
      },

      // ── B/C/D: isolation across list/search/get, superuser sees all ────────
      {
        name: '[B/C/D] Instances are isolated per tenant; superuser sees both',
        async fn() {
          const instA = await userA.startProcess('TenantProcess', {});
          const instanceIdA = instA.processInstanceId ?? instA.id;

          const instB = await userB.startProcess('TenantProcess', {});
          const instanceIdB = instB.processInstanceId ?? instB.id;

          // --- B: list endpoint (v1) ---
          const listA = await userA.api.get('/engine-rest/process-instance');
          const idsA = (listA.data as any[]).map(i => i.id);
          assert(idsA.includes(instanceIdA), 'tenant A should see its own instance in the list');
          assert(!idsA.includes(instanceIdB), 'tenant A should NOT see tenant B\'s instance in the list');

          const listB = await userB.api.get('/engine-rest/process-instance');
          const idsB = (listB.data as any[]).map(i => i.id);
          assert(idsB.includes(instanceIdB), 'tenant B should see its own instance in the list');
          assert(!idsB.includes(instanceIdA), 'tenant B should NOT see tenant A\'s instance in the list');

          // --- B: search endpoint (v2) ---
          const searchA = await userA.api.post('/v2/process-instances/search', {});
          const searchIdsA = (searchA.data.items as any[]).map(i => i.processInstanceKey);
          assert(searchIdsA.includes(instanceIdA), 'tenant A v2 search should include its own instance');
          assert(!searchIdsA.includes(instanceIdB), 'tenant A v2 search should NOT include tenant B\'s instance');

          // --- C: direct fetch by ID across tenants ---
          const crossFetch = await userA.getProcessInstance(instanceIdB);
          assert(crossFetch === null, 'tenant A fetching tenant B\'s instance by ID directly should 404/null');

          const crossFetchV2 = await userB.api.get(`/v2/process-instances/${instanceIdA}`).catch(e => e.response);
          assert(crossFetchV2.status === 404, `tenant B fetching tenant A's instance via v2 should 404, got ${crossFetchV2.status}`);

          // --- D: superuser sees both ---
          const listSuper = await superuser.api.get('/engine-rest/process-instance');
          const idsSuper = (listSuper.data as any[]).map(i => i.id);
          assert(idsSuper.includes(instanceIdA) && idsSuper.includes(instanceIdB),
            'superuser should see instances from both tenants');

          // --- process-definition listing isolation ---
          const defsA = await userA.api.get('/engine-rest/process-definition?key=TenantProcess');
          for (const def of defsA.data as any[]) {
            assert(def.tenantId === 'tenant-a', `tenant A should only see its own definitions, got tenantId=${def.tenantId}`);
          }
          const defsSuper = await superuser.api.get('/engine-rest/process-definition?key=TenantProcess');
          const tenantIdsSeen = new Set((defsSuper.data as any[]).map(d => d.tenantId));
          assert(tenantIdsSeen.has('tenant-a') && tenantIdsSeen.has('tenant-b'),
            'superuser should see process definitions from both tenants');
        },
      },

      // ── E: which definition a start runs ───────────────────────────────────
      {
        name: '[E] A tenant always runs its own version of a shared key, never another tenant\'s',
        async fn() {
          // The latest version of TenantProcess per tenant, as the superuser sees them.
          const all = (await superuser.api.get('/engine-rest/process-definition?key=TenantProcess')).data as any[];
          const latest = (tenant: string) => all.filter(d => d.tenantId === tenant).sort((x, y) => y.version - x.version)[0].id;
          const defA = latest('tenant-a');
          const defB = latest('tenant-b');
          const ok = { validateStatus: () => true };
          const base = '/engine-rest/process-definition/key/TenantProcess';

          // By key alone: tenant A's own version, whichever tenant deployed last.
          const byKey = await userA.api.post(`${base}/start`, {}, ok);
          assert(byKey.status === 200 && byKey.data.definitionId === defA && byKey.data.tenantId === 'tenant-a',
            `A by key must run A's definition ${defA}, got ${JSON.stringify(byKey.data)}`);

          // Its own tenant route: same, and the GET agrees.
          const own = await userA.api.post(`${base}/tenant-id/tenant-a/start`, {}, ok);
          assert(own.status === 200 && own.data.definitionId === defA, `A on tenant-a route: ${own.status} ${JSON.stringify(own.data)}`);
          const ownGet = await userA.api.get(`${base}/tenant-id/tenant-a`, ok);
          assert(ownGet.status === 200 && ownGet.data.id === defA && ownGet.data.tenantId === 'tenant-a', `A GET tenant-a: ${JSON.stringify(ownGet.data)}`);
          const form = await userA.api.post(`${base}/tenant-id/tenant-a/submit-form`, { variables: {} }, ok);
          assert(form.status === 200 && form.data.definitionId === defA, `A submit-form on tenant-a: ${form.status}`);

          // Another tenant's route: as if it did not exist.
          for (const [method, url] of [['post', `${base}/tenant-id/tenant-b/start`], ['get', `${base}/tenant-id/tenant-b`], ['post', `${base}/tenant-id/tenant-b/submit-form`]] as const) {
            const res = method === 'get' ? await userA.api.get(url, ok) : await userA.api.post(url, {}, ok);
            assert(res.status === 404, `A ${method.toUpperCase()} ${url}: expected 404, got ${res.status}`);
          }
          // Nor by definition id.
          const byId = await userA.api.post(`/engine-rest/process-definition/${defB}/start`, {}, ok);
          assert(byId.status === 404, `A starting B's definition by id: expected 404, got ${byId.status}`);

          // Unknown tenant: 404 for everyone.
          const unknown = await superuser.api.post(`${base}/tenant-id/no-such-tenant/start`, {}, ok);
          assert(unknown.status === 404, `unknown tenant: expected 404, got ${unknown.status}`);

          // The superuser may pick a tenant explicitly.
          const su = await superuser.api.post(`${base}/tenant-id/tenant-b/start`, {}, ok);
          assert(su.status === 200 && su.data.definitionId === defB && su.data.tenantId === 'tenant-b',
            `superuser on tenant-b route: ${JSON.stringify(su.data)}`);

          // The other start APIs resolve the same way.
          const legacy = await userA.api.post('/engine-rest/v2/process-definitions/TenantProcess/start', { variables: {} }, ok);
          assert(legacy.status < 300, `legacy start: ${legacy.status} ${JSON.stringify(legacy.data)}`);
          {
            const legacyId = legacy.data.processInstanceId ?? legacy.data.id ?? legacy.data.instanceId;
            const inst = (await superuser.api.get(`/engine-rest/process-instance/${legacyId}`)).data;
            assert(inst.definitionId === defA, `legacy start by A ran ${inst.definitionId}, want ${defA}`);
          }
          const v2 = await userA.api.post('/v2/process-instances', { processDefinitionId: 'TenantProcess' }, ok);
          assert(v2.status < 300, `v2 start: ${v2.status} ${JSON.stringify(v2.data)}`);
          const v2Inst = (await superuser.api.get(`/engine-rest/process-instance/${v2.data.processInstanceKey}`)).data;
          assert(v2Inst.definitionId === defA, `v2 start by A ran ${v2Inst.definitionId}, want ${defA}`);

          for (const r of [byKey, own, form, su]) {
            await superuser.api.delete(`/engine-rest/process-instance/${r.data.id}`, ok);
          }
          await superuser.api.delete(`/engine-rest/process-instance/${v2.data.processInstanceKey}`, ok);
        },
      },
      {
        name: '[F] A tenant cannot deploy into another tenant, nor see its tokens',
        async fn() {
          const fs = require('fs');
          const FormData = require('form-data');
          const form = new FormData();
          form.append('resources', fs.createReadStream(BPMN));
          form.append('deployment-name', 'cross-tenant');
          form.append('tenant-id', 'tenant-b');
          const res = await userA.api.post('/engine-rest/deployment/create', form, { headers: form.getHeaders(), validateStatus: () => true });
          assert(res.status === 403, `A deploying as tenant-b: expected 403, got ${res.status}`);

          // One instance per tenant, both waiting on reviewTask.
          const instId = (i: any) => (i.processInstanceId ?? i.id) as string;
          const a = instId(await userA.startProcess('TenantProcess', {}));
          const b = instId(await userB.startProcess('TenantProcess', {}));
          await superuser.waitForTask('reviewTask', a, 10000);
          await superuser.waitForTask('reviewTask', b, 10000);
          const positions = (await userA.api.get('/engine-rest/process-definition/key/TenantProcess/instance-positions')).data as any[];
          assert(positions.some(p => p.processInstanceId === a), 'A sees its own instance');
          assert(!positions.some(p => p.processInstanceId === b), `A must not see B's instance: ${JSON.stringify(positions)}`);
          const all = (await superuser.api.get('/engine-rest/process-definition/key/TenantProcess/instance-positions')).data as any[];
          assert(all.some(p => p.processInstanceId === a) && all.some(p => p.processInstanceId === b), 'the superuser sees both');
          for (const id of [a, b]) await superuser.api.delete(`/engine-rest/process-instance/${id}`);
        },
      },

    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
