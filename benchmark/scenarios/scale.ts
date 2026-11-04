import { Scenario } from '../src/scenario';

/**
 * The shapes the rest of the suite cannot cover, because every other fixture
 * is deliberately small: a model of realistic size (four-way parallel fork in
 * a sub-process, sequential multi-instance, two levels of sub-process), user
 * tasks carrying a form, candidate groups and a priority, and two chained DMN
 * tables with different hit policies.
 *
 * Same rule as everywhere else: nothing engine-specific, the same variables
 * take the same path on both engines.
 */

const done = { complete: {} };

const largeWorkers = {
  'large-check': () => ({ complete: { checked: true } }),
  'large-price': () => ({ complete: { priced: true } }),
  // One job type per parallel branch: with a shared type, whether the two
  // jobs arrive in one activation batch decides which variables they carry,
  // and that is a property of the harness, not of the model under test.
  'large-stock': () => ({ complete: { stocked: true } }),
  'large-risk': () => ({ complete: { risked: true } }),
  'large-manual': () => ({ complete: { approved: 'manual' } }),
  // large-auto is both the approval task and the charge task, in sequence.
  'large-auto': (_v: Record<string, any>, ctx: { nth: number }) =>
    ({ complete: ctx.nth === 1 ? { approved: 'auto' } : { charged: true } }),
  'large-item': (v: Record<string, any>) => ({ complete: { picked: v.item?.sku ?? null } }),
  // Three audit tasks share a type: number them so the order is visible.
  'large-audit': (_v: Record<string, any>, ctx: { nth: number }) =>
    ({ complete: { [`audited${ctx.nth}`]: true } }),
};

const items = [{ sku: 'A', qty: 1 }, { sku: 'B', qty: 2 }];

export const scale: Scenario[] = [
  {
    name: 'large-model-auto-approval', group: 'Large model',
    description: 'A 70-element model: four-way parallel fork in a sub-process, join, script task, default branch of an exclusive gateway, sequential multi-instance, then a nested audit sub-process that skips its second approver.',
    bpmn: ['large.bpmn'], process: 'large_order',
    variables: { amount: 500, tier: 'standard', items },
    workers: largeWorkers,
    unordered: true,          // the four intake branches arrive in no defined order
  },
  {
    name: 'large-model-manual-and-second-approver', group: 'Large model',
    description: 'The same model on the other path: total > 1000 takes the manual approval, and total > 2000 adds the second approver inside the audit sub-process.',
    bpmn: ['large.bpmn'], process: 'large_order',
    variables: { amount: 5000, tier: 'gold', items },
    workers: largeWorkers,
    unordered: true,
  },
  {
    name: 'user-task-form-candidate-groups', group: 'User tasks',
    description: 'A user task with a form, two candidate groups and a priority: it is found by /v2/user-tasks/search, completed, and its variables reach the next job.',
    bpmn: ['usertask_form.bpmn', 'approval.form'], process: 'ut_form',
    variables: { request: 'R-1' },
    workers: { 'uf-after': () => done },
    actions: [{ afterMs: 500, completeUserTask: { elementId: 'uf_approve', variables: { approved: true } } }],
  },
  {
    name: 'user-task-assigned-to-one-user', group: 'User tasks',
    description: 'The same user task shape, assigned to a single user instead of a candidate group.',
    bpmn: ['usertask_form.bpmn'], process: 'ut_assigned',
    variables: { request: 'R-2' },
    actions: [{ afterMs: 500, completeUserTask: { elementId: 'ua_approve', variables: { approved: false } } }],
  },
  {
    name: 'dmn-two-tables-platinum', group: 'DMN',
    description: 'Two chained decision tables (FIRST then UNIQUE): 1200 for a gold customer is platinum, and platinum ships free whatever the weight.',
    bpmn: ['dmn_two.bpmn', 'dmn_two_tables.dmn'], process: 'dmn_chain',
    variables: { amount: 1200, tier: 'gold', weight: 5 },
    workers: { 'dmn-show': v => ({ complete: { levelSeen: v.level, costSeen: v.cost } }) },
  },
  {
    name: 'dmn-two-tables-standard', group: 'DMN',
    description: 'The same tables on another row: 50 for a silver customer is standard, and standard ships at 9.5 whatever the weight.',
    bpmn: ['dmn_two.bpmn', 'dmn_two_tables.dmn'], process: 'dmn_chain',
    variables: { amount: 50, tier: 'silver', weight: 12 },
    workers: { 'dmn-show': v => ({ complete: { levelSeen: v.level, costSeen: v.cost } }) },
  },
];
