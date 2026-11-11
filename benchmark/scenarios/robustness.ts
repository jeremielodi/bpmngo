import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Robustness';
const R = 'robustness.bpmn';

// Big, nested, non-ASCII start variables (rb-big-unicode-variables).
const bigText = 'Lorem ipsum dolor sit amet, ✓ '.repeat(7000);            // ~200 KB
const unicode = 'Café ☕ — 日本語 — العربية — emoji 🚀👍🏽';
let deep: any = { level: 0 };
for (let i = 1; i <= 20; i++) deep = { level: i, child: deep };

export const robustness: Scenario[] = [
  {
    name: 'rb-loop-retries-until-ok', group,
    description: 'A loop back to an exclusive gateway: the task runs three times, until the worker says ok.',
    bpmn: [R], process: 'rb_loop',
    workers: { 'rb-attempt': (_v, ctx) => ({ complete: { ok: ctx.nth >= 3, attempts: ctx.nth } }) },
  },
  {
    name: 'rb-nested-subprocess', group,
    description: 'A sub-process inside a sub-process: tasks run inner, middle, outer; variables reach the instance.',
    bpmn: [R], process: 'rb_nested_sub',
    workers: {
      'rb-inner': () => ({ complete: { inner: 'done' } }),
      'rb-middle': v => ({ complete: { middleSaw: v.inner } }),
      'rb-outer': v => ({ complete: { outerSaw: v.middleSaw } }),
    },
  },
  {
    name: 'rb-subprocess-error-boundary', group,
    description: 'A job inside a sub-process throws RISK with variables: the error boundary on the sub-process catches it.',
    bpmn: [R], process: 'rb_sub_error',
    workers: {
      'rb-risky': () => ({ error: { code: 'RISK', message: 'over the limit', variables: { riskReason: 'limit' } } }),
      'rb-handle': v => ({ complete: { handled: v.riskReason ?? 'no reason' } }),
    },
  },
  {
    name: 'rb-subprocess-timer-boundary', group,
    description: 'A 1 s interrupting timer on a sub-process cancels the slow task inside; the timeout path runs; the late completion is refused.',
    bpmn: [R], process: 'rb_sub_timer',
    workers: { 'rb-slow': () => ({ later: 4000, then: { complete: { slow: true } } }), 'rb-timeout': () => ({ complete: { timedOut: true } }) },
  },
  {
    name: 'rb-subprocess-terminate-end', group,
    description: 'A terminate end inside a sub-process ends the sub-process only (the parallel task is cancelled); the flow after it runs.',
    bpmn: [R], process: 'rb_sub_terminate',
    workers: {
      'rb-cut': () => ({ later: 2500, then: { complete: { cut: false } } }),
      'rb-stop': () => ({ complete: { stopped: true } }),
      'rb-after-sub': () => ({ complete: { after: true } }),
    },
    unordered: true,
  },
  {
    name: 'rb-terminate-ends-instance', group,
    description: 'A terminate end at the top ends the instance at once; the job of the other branch is cancelled.',
    bpmn: [R], process: 'rb_terminate',
    workers: { 'rb-long': () => ({ later: 3000, then: { complete: {} } }), 'rb-quick': () => ({ complete: { quick: true } }) },
    unordered: true,
  },
  {
    name: 'rb-parallel-five-branches', group,
    description: 'Five parallel branches, each setting its own variable; after the join, a task sees all five.',
    bpmn: [R], process: 'rb_parallel_wide',
    workers: {
      'rb-w1': () => ({ complete: { w1: 1 } }), 'rb-w2': () => ({ complete: { w2: 2 } }), 'rb-w3': () => ({ complete: { w3: 3 } }),
      'rb-w4': () => ({ complete: { w4: 4 } }), 'rb-w5': () => ({ complete: { w5: 5 } }),
      'rb-wide-check': v => ({ complete: { total: (v.w1 ?? 0) + (v.w2 ?? 0) + (v.w3 ?? 0) + (v.w4 ?? 0) + (v.w5 ?? 0) } }),
    },
    unordered: true,
  },
  {
    name: 'rb-parallel-nested', group,
    description: 'A parallel split inside a parallel branch, two joins: the last task runs once, after the three tasks.',
    bpmn: [R], process: 'rb_parallel_nested',
    workers: {
      'rb-n-a': () => ({ complete: { a: true } }), 'rb-n-b1': () => ({ complete: { b1: true } }), 'rb-n-b2': () => ({ complete: { b2: true } }),
      'rb-n-last': v => ({ complete: { allThree: !!(v.a && v.b1 && v.b2) } }),
    },
    unordered: true,
  },
  {
    name: 'rb-inclusive-two-of-three', group,
    description: 'An inclusive split with two of three conditions true, then an inclusive join: it waits for exactly those two.',
    bpmn: [R], process: 'rb_inclusive_join', variables: { wantA: true, wantB: false, wantC: true },
    workers: {
      'rb-i-a': () => ({ complete: { a: 'done' } }), 'rb-i-b': () => ({ complete: { b: 'done' } }),
      'rb-i-c': () => ({ later: 1000, then: { complete: { c: 'done' } } }), 'rb-i-after': v => ({ complete: { joined: [v.a, v.b ?? null, v.c] } }),
    },
    unordered: true,
  },
  {
    name: 'rb-link-events', group,
    description: 'A link throw event jumps to the link catch event of the same name.',
    bpmn: [R], process: 'rb_links',
    workers: { 'rb-link-before': () => ({ complete: { before: true } }), 'rb-link-after': () => ({ complete: { after: true } }) },
  },
  {
    name: 'rb-two-end-events', group,
    description: 'Two parallel branches with their own end events: the instance ends when the slower one does.',
    bpmn: [R], process: 'rb_two_ends',
    workers: { 'rb-e-fast': () => ({ complete: { fast: true } }), 'rb-e-slow': () => ({ later: 1500, then: { complete: { slow: true } } }) },
    unordered: true,
  },
  {
    name: 'rb-script-task-feel', group,
    description: 'A FEEL script task sums qty × price over a list; the next job sees the result.',
    bpmn: [R], process: 'rb_script', variables: { lines: [{ qty: 2, price: 10.5 }, { qty: 1, price: 3 }, { qty: 4, price: 0.25 }] },
    workers: { 'rb-script-show': v => ({ complete: { seen: v.total } }) },
  },
  {
    name: 'rb-dmn-gold-customer', group,
    description: 'A business rule task evaluates a DMN table (ranges, string match): 500 for a gold customer gives 0.15.',
    bpmn: [R, 'robustness_discount.dmn'], process: 'rb_dmn', variables: { amount: 500, tier: 'gold' },
    workers: { 'rb-dmn-show': v => ({ complete: { applied: v.discount } }) },
  },
  {
    name: 'rb-dmn-big-order', group,
    description: 'The same table: 5000 for a standard customer gives 0.2; 50 would give 0.',
    bpmn: [R, 'robustness_discount.dmn'], process: 'rb_dmn', variables: { amount: 5000, tier: 'silver' },
    workers: { 'rb-dmn-show': v => ({ complete: { applied: v.discount } }) },
  },
  {
    name: 'rb-big-unicode-variables', group,
    description: 'A 200 KB string, non-ASCII text, a 20-level nested object and a 500-number list go through a job unchanged.',
    bpmn: [R], process: 'rb_big_vars',
    variables: { big: bigText, unicode, deep, numbers: Array.from({ length: 500 }, (_, i) => i * 1.5 - 100), nothing: null, flag: false },
    workers: {
      'rb-echo': v => ({ complete: { bigLength: String(v.big ?? '').length, unicodeBack: v.unicode, deepest: depth(v.deep), numbersSum: (v.numbers ?? []).reduce((a: number, b: number) => a + b, 0) } }),
    },
  },
  {
    name: 'rb-subprocess-escalation', group,
    description: 'An escalation thrown inside a sub-process is caught by a non-interrupting boundary: both paths run.',
    bpmn: [R], process: 'rb_sub_escalation',
    workers: { 'rb-esc-continue': () => ({ later: 1000, then: { complete: { continued: true } } }), 'rb-esc-notify': () => ({ complete: { notified: true } }) },
    unordered: true,
  },
  {
    name: 'rb-subprocess-error-end-caught', group,
    description: 'An invalid input reaches an error end event inside a sub-process: the boundary on the sub-process recovers.',
    bpmn: [R], process: 'rb_sub_error_end',
    workers: { 'rb-validate': () => ({ complete: { valid: false } }), 'rb-recover': () => ({ complete: { recovered: true } }) },
  },
  {
    name: 'rb-subprocess-error-end-not-reached', group,
    description: 'A valid input ends the sub-process normally: the error boundary does not fire.',
    bpmn: [R], process: 'rb_sub_error_end',
    workers: { 'rb-validate': () => ({ complete: { valid: true } }), 'rb-recover': () => ({ complete: { recovered: true } }) },
  },
  {
    name: 'rb-event-gateway-signal-wins', group,
    description: 'An event-based gateway waits for a signal or a 6 s timer: the signal comes after 1 s and wins.',
    bpmn: [R], process: 'rb_event_gw_signal',
    workers: { 'rb-by-signal': () => ({ complete: { by: 'signal' } }), 'rb-by-timer': () => ({ complete: { by: 'timer' } }) },
    actions: [{ afterMs: 1000, signal: { name: 'rb-go', variables: { signalled: true } } }],
  },
  {
    name: 'rb-message-nested-correlation-key', group,
    description: 'A message whose subscription correlates on order.id reaches the instance waiting for N-1.',
    bpmn: [R], process: 'rb_nested_key', variables: { order: { id: 'N-1', total: 99 } },
    workers: { 'rb-prepare': () => ({ complete: { prepared: true } }), 'rb-after-message': v => ({ complete: { paidSeen: v.paid ?? false } }) },
    actions: [{ afterJob: 'rb-prepare', publish: { name: 'rb-nested', correlationKey: 'N-1', variables: { paid: true } } }],
  },
  {
    name: 'rb-mi-subprocess-gateway', group,
    description: 'A multi-instance sub-process over [5, 20, 7] with a gateway inside: the output collection is small, big, small.',
    bpmn: [R], process: 'rb_mi_sub_gateway', variables: { items: [5, 20, 7] },
    workers: { 'rb-mi-big': () => done, 'rb-mi-small': () => done },
    unordered: true,
  },
  {
    name: 'rb-boundary-fires-twice', group,
    description: 'Two messages while the task runs: the non-interrupting boundary fires twice, the task still completes.',
    bpmn: [R], process: 'rb_boundary_twice', variables: { ref: 'R-1' },
    workers: { 'rb-bt-work': () => ({ later: 4000, then: { complete: { worked: true } } }), 'rb-bt-log': () => ({ complete: { logged: true } }) },
    actions: [
      { afterMs: 1000, publish: { name: 'rb-note', correlationKey: 'R-1', variables: { note: 1 } } },
      { afterMs: 2000, publish: { name: 'rb-note', correlationKey: 'R-1', variables: { note: 2 } } },
    ],
    unordered: true,
  },
  {
    name: 'rb-call-activity-child-timer', group,
    description: 'A call activity maps amount × 2 in; the child waits 1 s on a timer, works, and its result is mapped back out.',
    bpmn: [R], process: 'rb_call_timer', variables: { amount: 21 },
    workers: { 'rb-child-work': v => ({ complete: { childResult: (v.doubled ?? 0) + 1 } }), 'rb-call-after': v => ({ complete: { got: v.fromChild } }) },
  },
  {
    name: 'rb-modeler-export-freight', group,
    description: 'A model as Camunda Modeler exports it (diagram, documentation, properties, mappings): 35 kg in stock goes by freight.',
    bpmn: ['robustness_modeler.bpmn'], process: 'rb_modeler_order',
    variables: { order: { id: 'M-1', lines: [{ sku: 'A', kg: 20 }, { sku: 'B', kg: 15 }] } },
    workers: {
      'rb-md-stock': v => ({ complete: { available: Array.isArray(v.lines) && v.lines.length === 2 } }),
      'rb-md-reserve': () => ({ complete: { reserved: true } }),
      'rb-md-freight': () => ({ complete: { carrier: 'freight' } }),
      'rb-md-parcel': () => ({ complete: { carrier: 'parcel' } }),
      'rb-md-backorder': () => ({ complete: { backordered: true } }),
    },
  },
  {
    name: 'rb-modeler-export-backorder', group,
    description: 'The same exported model: out of stock takes the default flow to the back-order.',
    bpmn: ['robustness_modeler.bpmn'], process: 'rb_modeler_order',
    variables: { order: { id: 'M-2', lines: [{ sku: 'C', kg: 1 }] } },
    workers: {
      'rb-md-stock': () => ({ complete: { available: false } }),
      'rb-md-reserve': () => ({ complete: { reserved: true } }),
      'rb-md-freight': () => ({ complete: { carrier: 'freight' } }),
      'rb-md-parcel': () => ({ complete: { carrier: 'parcel' } }),
      'rb-md-backorder': () => ({ complete: { backordered: true } }),
    },
  },
];

function depth(o: any): number {
  let n = 0;
  while (o && typeof o === 'object' && o.child) { n++; o = o.child; }
  return n;
}
