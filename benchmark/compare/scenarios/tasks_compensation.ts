import { Scenario } from '../src/scenario';

const done = { complete: {} };

export const tasksCompensation: Scenario[] = [
  {
    name: 'user-task-completed-over-rest', group: 'User tasks',
    description: 'A Camunda user task is found with /v2/user-tasks/search and completed with /v2/user-tasks/{key}/completion; its variables reach the next job.',
    bpmn: ['tasks_compensation.bpmn'], process: 'ut_simple', variables: { request: 'R-1' },
    workers: { 'ut-after': () => done },
    actions: [{ afterMs: 500, completeUserTask: { elementId: 'u_approve', variables: { approved: true } } }],
  },
  {
    name: 'compensation-after-payment-error', group: 'Compensation',
    description: 'A payment error triggers compensation: the completed booking is undone (its handler sees the booking variables), then the failure path runs.',
    bpmn: ['tasks_compensation.bpmn'], process: 'cp_booking',
    workers: {
      'cp-book': () => ({ complete: { booking: 'B-1' } }),
      'cp-charge': () => ({ error: { code: 'PAY', message: 'card declined' } }),
      'cp-undo': v => ({ complete: { undone: v.booking ?? null } }),
      'cp-failed': () => done,
    },
  },
  {
    name: 'compensation-not-run-on-happy-path', group: 'Compensation',
    description: 'Payment succeeds: no compensation handler runs.',
    bpmn: ['tasks_compensation.bpmn'], process: 'cp_booking',
    workers: {
      'cp-book': () => ({ complete: { booking: 'B-2' } }),
      'cp-charge': () => ({ complete: { paid: true } }),
      'cp-undo': () => ({ complete: { undone: true } }),
      'cp-failed': () => done,
    },
  },
];
