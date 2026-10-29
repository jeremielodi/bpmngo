# Frontend

A React 19 + `bpmn-js` single-page app (`frontend/`), served by nginx in Docker. It covers a slice of Camunda's Operate and Tasklist, plus a BPMN modeler.

## Pages

| Page | Path | What it does |
|---|---|---|
| Dashboard | `/` | At-a-glance counts: running and completed instances, open tasks, incidents |
| Process Definitions | `/processes` | Deploy, browse, filter by key; manage each process's permission grants |
| Process Definition | `/processes/:id` | Read-only diagram with a live count of tokens per element, and the instances standing on it |
| Modeler | `/modeler` | Author a BPMN diagram in the browser and deploy it (see [Modeler](#modeler)) |
| Process Instances | `/instances` | Browse and filter instances by key and state |
| Instance Detail | `/instances/:id` | Live diagram, token replay, Move Token, Previous step (see [Instance Detail](#instance-detail)) |
| Tasks | `/tasks` | Task inbox: claim, unclaim, complete; open tasks by default |
| Incidents | `/incidents` | Incident list with retry and resolve |
| History | `/history` | Historic process instances, with filters |
| Analytics | `/analytics` | Duration distributions, throughput over time, incident rate per process key (Optimize-lite) |
| Users, Roles | `/admin/users`, `/admin/roles` | User and role management, role actions |

Every page shows only what the logged-in user may see: processes need a `VIEW` grant (see [process permissions](api.md#process-permissions)), and a tenant user sees only their tenant.

### Instance Detail

- A live BPMN diagram with an overlay on the active tokens.
- A **replay control**: play, pause, step and scrub through the instance's whole token history (derived from the audit log). A pulsing marker shows where the token was at each step, separate from the live overlay. Clicking a row of the Activity History timeline jumps the replay to that step.
- **Move Token** (process instance modification).
- **Previous step**: send a token back to the wait state before, with a preview of where it goes.

### Modeler

The `/modeler` page uses bpmn-js's editable `Modeler` build with a properties panel for the Zeebe task and gateway properties (job type, assignee, forms, conditions…). A diagram is deployed from there like any other deployment: the first deployment of a new process key makes the deployer its only manager.

## Running it

### With Docker (default)

`docker compose up -d --build --wait` starts the frontend with the rest of the stack (see [Quick Start](../readme.md#quick-start)). It is served on `http://localhost:${FRONTEND_PORT:-3000}`; nginx forwards the API paths (`/engine-rest`, `/v2`, `/auth`, `/users`, `/roles`, `/actions`, `/events`, `/ws`) to the BpmnGo container.

### In development

```bash
cd frontend
npm install
npm run dev
```

Vite serves the app on `http://localhost:3000` with hot reload and proxies the same API paths to a BpmnGo server on `http://localhost:8080` (`vite.config.ts`). Stop the dockerized frontend first (`docker compose stop frontend`): it uses port 3000 too.

A new API path must be added to both `vite.config.ts` and `nginx.conf`; one missing from either fails only in the browser, not in the API test suites.

## Tests

```bash
cd frontend
npm test            # unit tests (Vitest)
npm run test:e2e    # browser tests (Playwright) against a running frontend
```

The Playwright tests (`frontend/e2e/`) run against `http://localhost:3000` by default; set `BASE_URL` for another address. They go through nginx or Vite like a browser does, which is what catches a missing proxy path.

`npm run build` type-checks and builds the production bundle into `frontend/dist`.
