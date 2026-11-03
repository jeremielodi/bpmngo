import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Event sub-processes';

export const eventsub: Scenario[] = [
  {
    name: 'esp-message-interrupting', group,
    description: 'An interrupting message event sub-process cancels the main task and runs instead; the late completion is refused.',
    bpmn: ['eventsub.bpmn'], process: 'esp_message', variables: { orderId: 'E-1' },
    workers: { 'esp-work': () => ({ later: 4000, then: { complete: { worked: true } } }), 'esp-handle': () => ({ complete: { handled: true } }) },
    actions: [{ afterJob: 'esp-work', publish: { name: 'esp-cancel', correlationKey: 'E-1', variables: { why: 'cancel' } } }],
  },
  {
    name: 'esp-timer-non-interrupting', group,
    description: 'A non-interrupting 1 s timer event sub-process runs alongside; the instance ends when both are done.',
    bpmn: ['eventsub.bpmn'], process: 'esp_timer',
    workers: { 'esp-work': () => ({ later: 3000, then: { complete: { worked: true } } }), 'esp-tick': () => ({ complete: { ticked: true } }) },
    unordered: true,
  },
  {
    name: 'esp-error-catches-job-error', group,
    description: 'An error event sub-process catches the BPMN error a job throws.',
    bpmn: ['eventsub.bpmn'], process: 'esp_error',
    workers: { 'esp-work': () => ({ error: { code: 'X', variables: { failedAt: 'work' } } }), 'esp-handle': () => done },
  },
];
