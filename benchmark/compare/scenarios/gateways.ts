import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Gateways';

export const gateways: Scenario[] = [
  {
    name: 'gw-exclusive-default-taken', group,
    description: 'Exclusive gateway, no condition true: the default flow (listed first) is taken.',
    bpmn: ['gateways.bpmn'], process: 'gw_exclusive', variables: { amount: 50 },
    workers: { 'gw-small': () => done, 'gw-big': () => done, 'gw-huge': () => done },
  },
  {
    name: 'gw-exclusive-condition-beats-default', group,
    description: 'Exclusive gateway, one condition true: that flow, not the default listed before it.',
    bpmn: ['gateways.bpmn'], process: 'gw_exclusive', variables: { amount: 500 },
    workers: { 'gw-small': () => done, 'gw-big': () => done, 'gw-huge': () => done },
  },
  {
    name: 'gw-exclusive-two-conditions-true', group,
    description: 'Exclusive gateway, two conditions true: the first in document order.',
    bpmn: ['gateways.bpmn'], process: 'gw_exclusive', variables: { amount: 5000 },
    workers: { 'gw-small': () => done, 'gw-big': () => done, 'gw-huge': () => done },
  },
  {
    name: 'gw-exclusive-nothing-true-no-default', group,
    description: 'Exclusive gateway without default, nothing true: an incident on the gateway.',
    bpmn: ['gateways.bpmn'], process: 'gw_exclusive_nodefault', variables: { amount: 50 },
    workers: { 'gw-small': () => done, 'gw-big': () => done }, maxMs: 6000,
  },
  {
    name: 'gw-exclusive-condition-on-missing-variable', group,
    description: 'A condition on a variable that was never set (missing > 5), with a default flow.',
    bpmn: ['gateways.bpmn'], process: 'gw_exclusive_null', variables: {},
    workers: { 'gw-small': () => done, 'gw-big': () => done }, maxMs: 6000,
  },
  {
    name: 'gw-inclusive-default-alone', group,
    description: 'Inclusive gateway, nothing true: the default flow alone; the join waits only for it.',
    bpmn: ['gateways.bpmn'], process: 'gw_inclusive', variables: { wantA: false, wantB: false },
    workers: { 'gw-none': () => ({ complete: { none: true } }), 'gw-a': () => ({ complete: { a: true } }), 'gw-b': () => ({ complete: { b: true } }), 'gw-after': () => done },
    unordered: true,
  },
  {
    name: 'gw-inclusive-two-branches', group,
    description: 'Inclusive gateway, two conditions true: both branches, never the default; the join waits for both.',
    bpmn: ['gateways.bpmn'], process: 'gw_inclusive', variables: { wantA: true, wantB: true },
    workers: {
      'gw-none': () => ({ complete: { none: true } }),
      'gw-a': () => ({ complete: { a: true } }),
      'gw-b': () => ({ later: 1500, then: { complete: { b: true } } }),
      'gw-after': () => done,
    },
    unordered: true,
  },
  {
    name: 'gw-inclusive-nothing-true-no-default', group,
    description: 'Inclusive gateway without default, nothing true: an incident on the gateway.',
    bpmn: ['gateways.bpmn'], process: 'gw_inclusive_nodefault', variables: { wantA: false, wantB: false },
    workers: { 'gw-a': () => done, 'gw-b': () => done }, maxMs: 6000,
  },
  {
    name: 'gw-parallel-join-merges-variables', group,
    description: 'Parallel fork/join: three branches set their own variable; the task after the join sees all three.',
    bpmn: ['gateways.bpmn'], process: 'gw_parallel', variables: { order: 'O-1' },
    workers: {
      'gw-a': () => ({ complete: { a: 1 } }),
      'gw-b': () => ({ later: 500, then: { complete: { b: 2 } } }),
      'gw-c': () => ({ later: 1000, then: { complete: { c: 3 } } }),
      'gw-after': () => done,
    },
    unordered: true,
  },
];
