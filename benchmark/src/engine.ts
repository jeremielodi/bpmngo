/**
 * engine.ts — one engine under test, reached exactly as a Camunda 8 client
 * reaches it: the Zeebe gRPC gateway (official proto, proto/zeebe.proto)
 * plus the /v2 REST API for user tasks and read-only searches.
 *
 * The same class drives Camunda and BpmnGo; only the addresses and the
 * credentials differ.
 */
import * as path from 'path';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

export interface EngineConfig {
  name: string;            // "camunda" or "bpmngo" (used in file names)
  grpc: string;            // host:port of the Zeebe gateway
  rest: string;            // base URL of the REST API, e.g. http://localhost:8080
  user?: string;           // Basic credentials, sent on gRPC and REST when set
  password?: string;
}

/** A gRPC failure, reduced to what is comparable between engines. */
export interface CallError {
  code: string;            // gRPC status name, e.g. NOT_FOUND
  message: string;
}

const PROTO = path.join(__dirname, '..', 'proto', 'zeebe.proto');

export class Engine {
  readonly cfg: EngineConfig;
  private client: any;
  private auth?: string;

  constructor(cfg: EngineConfig) {
    this.cfg = cfg;
    const def = protoLoader.loadSync(PROTO, { keepCase: true, longs: String, enums: String, defaults: true, oneofs: true });
    const proto = grpc.loadPackageDefinition(def) as any;
    if (cfg.user) this.auth = 'Basic ' + Buffer.from(`${cfg.user}:${cfg.password ?? ''}`).toString('base64');
    const interceptors = this.auth ? [authInterceptor(this.auth)] : [];
    this.client = new proto.gateway_protocol.Gateway(cfg.grpc, grpc.credentials.createInsecure(), {
      interceptors,
      'grpc.max_receive_message_length': 16 * 1024 * 1024,
    });
  }

  close() {
    this.client.close();
  }

  /** One unary gRPC call; rejects with a CallError. */
  call<T = any>(method: string, req: unknown, deadlineMs = 30000): Promise<T> {
    return new Promise((resolve, reject) => {
      this.client[method](req, { deadline: Date.now() + deadlineMs }, (err: grpc.ServiceError | null, res: T) => {
        if (err) reject(toCallError(err));
        else resolve(res);
      });
    });
  }

  /** ActivateJobs is server-streaming: collects every job the gateway sends. */
  activateJobs(type: string, maxJobs = 32, requestTimeoutMs = 500): Promise<ActivatedJob[]> {
    return new Promise((resolve, reject) => {
      const jobs: ActivatedJob[] = [];
      const stream = this.client.ActivateJobs(
        { type, worker: 'bpmn-compare', timeout: 60000, maxJobsToActivate: maxJobs, requestTimeout: requestTimeoutMs, fetchVariable: [], tenantIds: [] },
        { deadline: Date.now() + requestTimeoutMs + 10000 },
      );
      stream.on('data', (res: any) => {
        for (const j of res.jobs ?? []) {
          jobs.push({
            key: String(j.key),
            type: j.type,
            processInstanceKey: String(j.processInstanceKey),
            bpmnProcessId: j.bpmnProcessId,
            elementId: j.elementId,
            retries: j.retries,
            customHeaders: parseJson(j.customHeaders) ?? {},
            variables: parseJson(j.variables) ?? {},
          });
        }
      });
      stream.on('end', () => resolve(jobs));
      stream.on('error', (err: grpc.ServiceError) => {
        // A long poll that simply found nothing may end with DEADLINE_EXCEEDED.
        if (err.code === grpc.status.DEADLINE_EXCEEDED || err.code === grpc.status.CANCELLED) resolve(jobs);
        else reject(toCallError(err));
      });
    });
  }

  async deploy(fileName: string, content: Buffer) {
    return this.call('DeployResource', { resources: [{ name: fileName, content }], tenantId: '' });
  }

  // ── REST ──────────────────────────────────────────────────────────────────

  async rest(method: 'GET' | 'POST', urlPath: string, body?: unknown): Promise<{ status: number; data: any }> {
    const headers: Record<string, string> = { accept: 'application/json' };
    if (body !== undefined) headers['content-type'] = 'application/json';
    if (this.auth) headers.authorization = this.auth;
    try {
      const res = await fetch(this.cfg.rest.replace(/\/$/, '') + urlPath, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(15000),
      });
      const text = await res.text();
      return { status: res.status, data: parseJson(text) ?? text };
    } catch (e: any) {
      return { status: 0, data: String(e?.message ?? e) };
    }
  }
}

export interface ActivatedJob {
  key: string;
  type: string;
  processInstanceKey: string;
  bpmnProcessId: string;
  elementId: string;
  retries: number;
  customHeaders: Record<string, unknown>;
  variables: Record<string, unknown>;
}

function authInterceptor(authorization: string) {
  return (options: any, nextCall: any) => new grpc.InterceptingCall(nextCall(options), {
    start(metadata: grpc.Metadata, listener: any, next: any) {
      metadata.set('authorization', authorization);
      next(metadata, listener);
    },
  });
}

function toCallError(err: grpc.ServiceError): CallError {
  const name = Object.entries(grpc.status).find(([, v]) => v === err.code)?.[0] ?? String(err.code);
  return { code: name, message: err.details || err.message };
}

export function parseJson(s: unknown): any {
  if (typeof s !== 'string' || s === '') return undefined;
  try {
    return JSON.parse(s);
  } catch {
    return undefined;
  }
}
