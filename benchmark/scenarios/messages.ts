import { Scenario } from '../src/scenario';

const done = { complete: {} };
const group = 'Messages';

export const messages: Scenario[] = [
  {
    name: 'msg-correlation-key', group,
    description: 'A message with another key does nothing; the one with the instance\'s key resumes it and merges its variables.',
    bpmn: ['messages.bpmn'], process: 'msg_catch', variables: { orderId: 'A1' },
    workers: { 'msg-prep': () => done, 'msg-after': () => done },
    actions: [
      { afterJob: 'msg-prep', afterMs: 1500, publish: { name: 'msg-paid', correlationKey: 'OTHER', variables: { wrong: true } } },
      { afterJob: 'msg-prep', afterMs: 3000, publish: { name: 'msg-paid', correlationKey: 'A1', variables: { paidBy: 'card' } } },
    ],
  },
  {
    name: 'msg-ttl-buffered', group,
    description: 'A message published before the instance waits for it, with a 10 s time to live, is correlated when the catch event is reached.',
    bpmn: ['messages.bpmn'], process: 'msg_catch', variables: { orderId: 'A2' },
    workers: { 'msg-prep': () => ({ later: 2000, then: done }), 'msg-after': () => done },
    actions: [{ afterMs: 0, publish: { name: 'msg-paid', correlationKey: 'A2', ttlMs: 10000, variables: { early: true } } }],
  },
  {
    name: 'msg-ttl-zero-not-kept', group,
    description: 'The same with a time to live of 0: the message is not kept, the instance waits.',
    bpmn: ['messages.bpmn'], process: 'msg_catch', variables: { orderId: 'A3' },
    workers: { 'msg-prep': () => ({ later: 2000, then: done }), 'msg-after': () => done },
    actions: [{ afterMs: 0, publish: { name: 'msg-paid', correlationKey: 'A3', ttlMs: 0 } }],
    maxMs: 8000,
  },
  {
    name: 'msg-duplicate-message-id', group,
    description: 'Publishing twice with the same messageId while the first is kept: the second is refused.',
    bpmn: ['messages.bpmn'], process: 'msg_catch', variables: { orderId: 'A4' },
    workers: { 'msg-prep': () => ({ later: 2000, then: done }), 'msg-after': () => done },
    actions: [
      { afterMs: 0, publish: { name: 'msg-paid', correlationKey: 'A4', ttlMs: 10000, messageId: 'm-1', variables: { copy: 1 } } },
      { afterMs: 300, publish: { name: 'msg-paid', correlationKey: 'A4', ttlMs: 10000, messageId: 'm-1', variables: { copy: 2 } } },
    ],
  },
  {
    name: 'msg-start-event', group,
    description: 'A message start event starts an instance with the message variables.',
    bpmn: ['messages.bpmn'], process: 'msg_start',
    startByMessage: { name: 'msg-begin', correlationKey: 'S1', variables: { source: 'message' } },
    workers: { 'msg-start-work': () => done },
  },
  {
    name: 'msg-event-gateway-message-wins', group,
    description: 'Event-based gateway: the message arrives after 1 s, before the 3 s timer; the timer never fires.',
    bpmn: ['messages.bpmn'], process: 'msg_ebg', variables: { orderId: 'G1' },
    workers: { 'msg-approved': () => done, 'msg-timeout': () => done },
    actions: [{ afterMs: 1000, publish: { name: 'msg-approve', correlationKey: 'G1' } }],
  },
  {
    name: 'msg-event-gateway-timer-wins', group,
    description: 'Event-based gateway: no message; the 3 s timer wins.',
    bpmn: ['messages.bpmn'], process: 'msg_ebg', variables: { orderId: 'G2' },
    workers: { 'msg-approved': () => done, 'msg-timeout': () => done },
  },
];
