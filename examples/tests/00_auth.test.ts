/**
 * Suite 00 — Authentication
 * Tests: login, token usage, refresh, logout, invalid credentials
 */
import axios from 'axios';
import * as path from 'path';
import { BpmnGoClient, BASE_URL, runSuite, assert } from './client';

const client = new BpmnGoClient();
// No token, no interceptor: what an outsider sees.
const anonymous = axios.create({ baseURL: BASE_URL, validateStatus: () => true });

async function run() {
  await runSuite({
    name: '00 — Authentication',
    tests: [
      {
        name: 'Login with valid credentials returns access + refresh token',
        async fn() {
          const res = await client.login('admin@goflow.com', 'admin123');
          assert(typeof res.access_token === 'string' && res.access_token.length > 0, 'access_token missing');
          assert(typeof res.refresh_token === 'string' && res.refresh_token.length > 0, 'refresh_token missing');
        },
      },
      {
        name: 'Authenticated request to /users/me returns current user',
        async fn() {
          await client.loginAsSuperUser();
          const res = await client.api.get('/users/me');
          assert(res.data.user?.email === 'admin@goflow.com', 'wrong email returned');
        },
      },
      {
        name: 'Refresh token returns new access + refresh tokens',
        async fn() {
          const first = await client.loginAsSuperUser();
          const refreshed = await client.refreshToken(first.refresh_token);
          assert(typeof refreshed.access_token === 'string', 'new access_token missing');
          assert(
            refreshed.access_token !== first.access_token,
            'new access_token should differ from the old one'
          );
        },
      },
      {
        name: 'Logout invalidates the session',
        async fn() {
          await client.loginAsSuperUser();
          await client.logout();
          // After logout the token is cleared; unauthenticated calls to protected
          // routes must fail with 401.
          const res = await anonymous.get('/users/me');
          assert(res.status === 401, `expected 401 after logout, got ${res.status}`);
          // The client really did drop its token: its own call fails too.
          const own = await client.api.get('/users/me', { validateStatus: () => true });
          assert(own.status === 401, `expected 401 after logout, got ${own.status}`);
        },
      },
      {
        name: 'A process key containing a public prefix ("app…") still requires auth',
        async fn() {
          await client.loginAsSuperUser();
          await client.deployBpmn(path.join(__dirname, 'bpmn/00_approval_key.bpmn'));
          const key = 'approval00';
          const get = await anonymous.get(`/engine-rest/process-definition/key/${key}`);
          assert(get.status === 401, `anonymous GET definition: expected 401, got ${get.status}`);
          const start = await anonymous.post(`/engine-rest/process-definition/key/${key}/start`, { variables: {} });
          assert(start.status === 401, `anonymous start: expected 401, got ${start.status}`);
          // Same path with a token works, so the 401 above is the auth layer.
          const ok = await client.api.get(`/engine-rest/process-definition/key/${key}`);
          assert(ok.data.key === key, 'authenticated GET should return the definition');
        },
      },
      {
        name: 'Task event streams refuse anonymous clients',
        async fn() {
          const sse = await anonymous.get('/events/tasks', { timeout: 5000 });
          assert(sse.status === 401, `anonymous SSE: expected 401, got ${sse.status}`);
          const ws = await anonymous.get('/ws/tasks', {
            headers: { Connection: 'Upgrade', Upgrade: 'websocket', 'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==' },
            timeout: 5000,
          });
          assert(ws.status === 401, `anonymous WebSocket upgrade: expected 401, got ${ws.status}`);
        },
      },
      {
        name: 'Login with wrong password returns 401',
        async fn() {
          try {
            await client.login('admin@goflow.com', 'wrong_password');
            throw new Error('Expected error was not thrown');
          } catch (e: any) {
            assert(e.response?.status === 401 || e.response?.status === 400,
              `expected 401/400, got ${e.response?.status}`);
          }
        },
      },
    ],
  });
}

run().catch(err => { console.error(err); process.exit(1); });
