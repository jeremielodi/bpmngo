import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Call activities';

export const callActivity: Scenario[] = [
  {
    name: 'call-propagate-all', group,
    description: 'Default propagation: the child sees the parent\'s variables; every child variable comes back.',
    bpmn: ['call_activity.bpmn'], process: 'ca_parent_all', variables: { amount: 21, label: 'P' },
    workers: { 'ca-child-work': v => ({ complete: { childResult: v.amount * 2, childOnly: true } }), 'ca-after': () => done },
  },
  {
    name: 'call-only-mappings', group,
    description: 'propagateAll* = false: the child sees only the input mapping; only the output mapping comes back.',
    bpmn: ['call_activity.bpmn'], process: 'ca_parent_mapped', variables: { amount: 21, secret: 'parent only' },
    workers: { 'ca-child-work': v => ({ complete: { childResult: (v.given ?? 0) + 1, noise: 1 } }), 'ca-after': () => done },
  },
  {
    name: 'call-child-error-caught-by-parent', group,
    description: 'A BPMN error thrown in the child, uncaught there, is caught by the error boundary on the call activity.',
    bpmn: ['call_activity.bpmn'], process: 'ca_parent_error', variables: { amount: 1 },
    workers: { 'ca-child-work': () => ({ error: { code: 'CHILD_ERR', variables: { fromChild: true } } }), 'ca-on-error': () => done },
  },
];
