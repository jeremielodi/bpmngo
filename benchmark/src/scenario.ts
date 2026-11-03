/**
 * scenario.ts — how a comparison scenario is described.
 *
 * A scenario is one BPMN file plus what the "outside world" does while an
 * instance runs: how workers answer each job, and which messages/signals
 * are sent and when. The runner plays it identically on every engine.
 */

/** What a worker does with a job. */
export type JobAnswer =
  | { complete: Record<string, unknown> }
  | { fail: { retries: number; message?: string; backoffMs?: number; variables?: Record<string, unknown> } }
  | { error: { code: string; message?: string; variables?: Record<string, unknown> } }
  | { ignore: true }                                   // leave it locked (lets a timer win)
  | { later: number; then: JobAnswer };               // answer after `later` ms (the job may be gone by then)

/** Context given to a worker: how many jobs of this type it already saw. */
export interface JobContext {
  nth: number;                                        // 1 for the first job of this type
}

export type Worker = (variables: Record<string, any>, ctx: JobContext) => JobAnswer;

/** An action the outside world takes during the run. */
export interface Action {
  /** Trigger: after the Nth job of a type was answered, or N ms after start. */
  afterJob?: string;
  nth?: number;                                       // with afterJob; default 1
  afterMs?: number;
  /** Then do one of: */
  publish?: { name: string; correlationKey?: string; ttlMs?: number; messageId?: string; variables?: Record<string, unknown> };
  signal?: { name: string; variables?: Record<string, unknown> };
  setVariables?: { variables: Record<string, unknown>; local?: boolean };
  cancel?: true;
  completeUserTask?: { elementId: string; variables?: Record<string, unknown> };
}

export interface Scenario {
  name: string;                                       // unique, kebab-case
  group: string;                                      // for the report
  description: string;
  bpmn: string[];                                     // files in bpmn/, first one holds `process`
  process: string;                                    // bpmnProcessId to start
  variables?: Record<string, unknown>;                // start variables
  workers?: Record<string, Worker>;                   // job type -> answer
  actions?: Action[];
  /** How long the instance may take before the run stops waiting. */
  maxMs?: number;
  /** Jobs of parallel branches arrive in no defined order: compare sorted. */
  unordered?: boolean;
  /** Start without waiting for the result (needed when the scenario cancels). */
  plainStart?: boolean;
  /** Start by publishing this message instead of CreateProcessInstance. */
  startByMessage?: { name: string; correlationKey?: string; variables?: Record<string, unknown> };
  /** Start by broadcasting this signal instead of CreateProcessInstance. */
  startBySignal?: { name: string; variables?: Record<string, unknown> };
}
