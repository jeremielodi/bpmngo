import { Scenario } from '../src/scenario';

const done = { complete: {} };

export const signalsTimers: Scenario[] = [
  {
    name: 'signal-start-event', group: 'Signals',
    description: 'A signal start event starts an instance with the signal variables.',
    bpmn: ['signals_timers.bpmn'], process: 'sig_start',
    startBySignal: { name: 'sig-go', variables: { reason: 'signal' } },
    workers: { 'sig-work': () => done },
  },
  {
    name: 'signal-intermediate-catch', group: 'Signals',
    description: 'An instance waiting at a signal catch event is resumed by a broadcast, with the signal variables.',
    bpmn: ['signals_timers.bpmn'], process: 'sig_catch',
    workers: { 'sig-prep': () => done, 'sig-after': () => done },
    actions: [{ afterJob: 'sig-prep', signal: { name: 'sig-continue', variables: { resumedBy: 'signal' } } }],
  },
  {
    name: 'timer-intermediate-duration', group: 'Timers',
    description: 'An intermediate PT1S timer.',
    bpmn: ['signals_timers.bpmn'], process: 'tm_duration',
    workers: { 'tm-after': () => done },
  },
  {
    name: 'timer-date-in-the-past', group: 'Timers',
    description: 'A timeDate already past fires at once.',
    bpmn: ['signals_timers.bpmn'], process: 'tm_date_past',
    workers: { 'tm-after': () => done },
  },
  {
    name: 'timer-duration-from-feel', group: 'Timers',
    description: 'A timer duration from a FEEL expression (=delay, "PT1S").',
    bpmn: ['signals_timers.bpmn'], process: 'tm_feel', variables: { delay: 'PT1S' },
    workers: { 'tm-after': () => done },
  },
  {
    name: 'timer-cycle-boundary-fires-twice', group: 'Timers',
    description: 'A non-interrupting R2/PT1S boundary fires twice while the task runs for 4.5 s.',
    bpmn: ['signals_timers.bpmn'], process: 'tm_cycle',
    workers: { 'tm-work': () => ({ later: 4500, then: done }), 'tm-reminder': (_v, ctx) => ({ complete: { [`reminder${ctx.nth}`]: true } }) },
    unordered: true,
  },
];
