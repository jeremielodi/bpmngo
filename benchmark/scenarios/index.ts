import { Scenario } from '../src/scenario';
import { boundary } from './boundary';
import { callActivity } from './call_activity';
import { eventsub } from './eventsub';
import { feel } from './feel';
import { gateways } from './gateways';
import { jobs } from './jobs';
import { messages } from './messages';
import { multiInstance } from './multi_instance';
import { signalsTimers } from './signals_timers';
import { tasksCompensation } from './tasks_compensation';
import { robustness } from './robustness';

export const SCENARIOS: Scenario[] = [
  ...gateways,
  ...boundary,
  ...jobs,
  ...eventsub,
  ...multiInstance,
  ...callActivity,
  ...messages,
  ...signalsTimers,
  ...feel,
  ...tasksCompensation,
  ...robustness,
];

// Scenario names must be unique: they are the trace file names.
const names = new Set<string>();
for (const s of SCENARIOS) {
  if (names.has(s.name)) throw new Error(`duplicate scenario name: ${s.name}`);
  names.add(s.name);
}
