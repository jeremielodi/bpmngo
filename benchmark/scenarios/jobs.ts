import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Jobs and variables';

export const jobs: Scenario[] = [
  {
    name: 'job-fail-then-retry', group,
    description: 'The worker fails the job leaving 2 retries; the job comes back with retries = 2 and is completed.',
    bpmn: ['jobs.bpmn'], process: 'job_retry',
    workers: { 'job-work': (_v, ctx) => (ctx.nth === 1 ? { fail: { retries: 2, message: 'temporary' } } : { complete: { attempt: ctx.nth } }) },
  },
  {
    name: 'job-fail-no-retries-left', group,
    description: 'The worker fails the job with 0 retries left: an incident on the task.',
    bpmn: ['jobs.bpmn'], process: 'job_retry',
    workers: { 'job-work': () => ({ fail: { retries: 0, message: 'permanent' } }) }, maxMs: 6000,
  },
  {
    name: 'job-fail-with-backoff', group,
    description: 'Failed with a 2 s retry backoff: the job is not handed out again before then.',
    bpmn: ['jobs.bpmn'], process: 'job_retry',
    workers: { 'job-work': (_v, ctx) => (ctx.nth === 1 ? { fail: { retries: 1, backoffMs: 2000 } } : done) },
  },
  {
    name: 'job-fail-with-variables', group,
    description: 'FailJob carries variables: they are set on the task scope and seen by the retry.',
    bpmn: ['jobs.bpmn'], process: 'job_retry',
    workers: { 'job-work': (v, ctx) => (ctx.nth === 1 ? { fail: { retries: 2, variables: { lastError: 'E1' } } } : { complete: { sawLastError: v.lastError ?? null } }) },
  },
  {
    name: 'job-headers-and-io-mappings', group,
    description: 'Task headers reach the worker; input mappings compute local variables; only the output mapping target comes back.',
    bpmn: ['jobs.bpmn'], process: 'job_io', variables: { amount: 21 },
    workers: { 'job-calc': () => ({ complete: { result: 41 } }), 'job-check': () => done },
  },
  {
    name: 'job-variable-types', group,
    description: 'A worker sets one variable of each JSON type; the next job and the result see them unchanged.',
    bpmn: ['jobs.bpmn'], process: 'job_types',
    workers: {
      'job-set': () => ({ complete: { n: 1, f: 1.5, neg: -3, big: 9007199254740991, s: 'é ✓', b: true, nul: null, arr: [1, 'a', null], obj: { k: { z: [1, { y: 2 }] } }, empty: {} } }),
      'job-read': () => done,
    },
  },
];
