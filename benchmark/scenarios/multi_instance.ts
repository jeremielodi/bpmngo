import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Multi-instance';

export const multiInstance: Scenario[] = [
  {
    name: 'mi-parallel-output-collection', group,
    description: 'Parallel loop over [3, 1, 2], completed in a different order than created: results keep the input order.',
    bpmn: ['multi_instance.bpmn'], process: 'mi_parallel', variables: { items: [3, 1, 2] },
    workers: {
      'mi-each': v => ({ later: v.item * 400, then: { complete: { result: v.item * 10 } } }),
      'mi-after': () => done,
    },
    unordered: true,
  },
  {
    name: 'mi-sequential-loop-counter', group,
    description: 'Sequential loop: one job at a time, each with its element and loopCounter 1, 2, 3.',
    bpmn: ['multi_instance.bpmn'], process: 'mi_sequential', variables: { names: ['a', 'b', 'c'] },
    workers: { 'mi-each': v => ({ complete: { [`seen_${v.name}`]: v.loopCounter } }) },
  },
  {
    name: 'mi-completion-condition', group,
    description: 'Completion condition numberOfCompletedInstances >= 2: the third job is cancelled and its late completion refused.',
    bpmn: ['multi_instance.bpmn'], process: 'mi_condition', variables: { items: [1, 2, 3] },
    workers: {
      'mi-each': v => (v.item === 3 ? { later: 4000, then: { complete: { result: 30 } } } : { later: v.item * 300, then: { complete: { result: v.item * 10 } } }),
      'mi-after': () => done,
    },
    unordered: true,
  },
  {
    name: 'mi-empty-collection', group,
    description: 'An empty input collection: the activity is skipped and the output collection is empty.',
    bpmn: ['multi_instance.bpmn'], process: 'mi_parallel', variables: { items: [] },
    workers: { 'mi-each': () => ({ complete: { result: 0 } }), 'mi-after': () => done },
  },
  {
    name: 'mi-input-not-a-list', group,
    description: 'The input collection is a number, not a list: an incident.',
    bpmn: ['multi_instance.bpmn'], process: 'mi_parallel', variables: { items: 5 },
    workers: { 'mi-each': () => ({ complete: { result: 0 } }), 'mi-after': () => done }, maxMs: 6000,
  },
  {
    name: 'mi-subprocess-local-variables', group,
    description: 'Multi-instance sub-process with a local input mapping; locals do not leak; the output collection reads them.',
    bpmn: ['multi_instance.bpmn'], process: 'mi_subprocess', variables: { items: [1, 2, 3] },
    workers: { 'mi-inner': v => ({ later: v.item * 300, then: { complete: { bonus: 1 } } }), 'mi-after': () => done },
    unordered: true,
  },
];
