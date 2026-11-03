import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Boundary events';

export const boundary: Scenario[] = [
  {
    name: 'boundary-timer-interrupting', group,
    description: 'A 2 s interrupting timer on a service task whose worker answers after 4 s: the timer path runs, the late completion is refused.',
    bpmn: ['boundary.bpmn'], process: 'be_timer_int',
    workers: { 'be-slow': () => ({ later: 4000, then: { complete: { slowDone: true } } }), 'be-late': () => done },
  },
  {
    name: 'boundary-timer-non-interrupting', group,
    description: 'A 1 s non-interrupting timer: the reminder runs and the task still completes after 3 s.',
    bpmn: ['boundary.bpmn'], process: 'be_timer_nonint',
    workers: { 'be-long': () => ({ later: 3000, then: { complete: { longDone: true } } }), 'be-reminder': () => ({ complete: { reminded: true } }) },
    unordered: true,
  },
  {
    name: 'boundary-message-interrupting', group,
    description: 'A message with the instance\'s correlation key interrupts the task; the boundary path runs; the late completion is refused.',
    bpmn: ['boundary.bpmn'], process: 'be_message_int', variables: { orderId: 'O-7' },
    workers: { 'be-work': () => ({ later: 4000, then: { complete: { worked: true } } }), 'be-cancelled': () => done },
    actions: [{ afterJob: 'be-work', publish: { name: 'be-cancel', correlationKey: 'O-7', variables: { reason: 'customer' } } }],
  },
  {
    name: 'boundary-error-specific-code', group,
    description: 'The job throws CARD: the boundary for CARD catches it, not the catch-all.',
    bpmn: ['boundary.bpmn'], process: 'be_error',
    workers: { 'be-pay': () => ({ error: { code: 'CARD', message: 'declined', variables: { declinedBy: 'bank' } } }), 'be-on-card': () => done, 'be-on-any': () => done },
  },
  {
    name: 'boundary-error-catch-all', group,
    description: 'The job throws another code: the catch-all boundary (no errorRef) catches it.',
    bpmn: ['boundary.bpmn'], process: 'be_error',
    workers: { 'be-pay': () => ({ error: { code: 'TIMEOUT' } }), 'be-on-card': () => done, 'be-on-any': () => done },
  },
  {
    name: 'boundary-error-uncaught', group,
    description: 'The job throws an error nothing catches: an incident.',
    bpmn: ['boundary.bpmn'], process: 'be_error_uncaught',
    workers: { 'be-pay': () => ({ error: { code: 'NOPE', message: 'nobody catches this' } }) }, maxMs: 6000,
  },
  {
    name: 'boundary-signal-interrupting', group,
    description: 'A broadcast signal interrupts the task; the boundary path runs; the late completion is refused.',
    bpmn: ['boundary.bpmn'], process: 'be_signal_int',
    workers: { 'be-work': () => ({ later: 4000, then: done }), 'be-stopped': () => done },
    actions: [{ afterJob: 'be-work', signal: { name: 'be-stop', variables: { stoppedBy: 'signal' } } }],
  },
  {
    name: 'boundary-escalation-non-interrupting', group,
    description: 'An escalation end event inside a sub-process, caught by a non-interrupting boundary: both paths run.',
    bpmn: ['boundary.bpmn'], process: 'be_escalation',
    workers: { 'be-note': () => ({ complete: { noted: true } }), 'be-after': () => done, 'be-on-late': () => done },
    unordered: true,
  },
];
