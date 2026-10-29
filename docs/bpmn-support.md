# BPMN 2.0 Support

Which BPMN 2.0 elements BpmnGo runs, with Camunda 8 semantics. Back to the [README](../README.md).

| Element | Status |
|---|---|
| Start Event: none, message, signal, timer (`timeDate`, `timeCycle` as ISO 8601 `R[n]/[start/]duration[/end]` or cron; armed on deploy, replaced by each new version) | ✅ |
| End Event | ✅ |
| Service Task (External Worker) | ✅ |
| User Task (with assignee / candidateGroups) | ✅ |
| Script Task (`zeebe:script` FEEL expression, or a job worker) | ✅ |
| Messages with Camunda 8 semantics: `zeebe:subscription` correlation keys, time to live (buffered messages), `messageId` deduplication, once per process, message start events (one active instance per key) | ✅ |
| Call Activity: `zeebe:calledElement` (processId as FEEL, `propagateAllChildVariables`, `propagateAllParentVariables`), input/output mappings, Camunda 7 `camunda:in`/`camunda:out`; uncaught errors and escalations rise to the call activity; the called instance is terminated with its call activity | ✅ |
| Exclusive Gateway (XOR): when several conditions are true, the first outgoing flow in the file is taken, as the BPMN 2.0 standard says (Camunda 8.10 was seen taking the last) | ✅ |
| Inclusive Gateway (OR) | ✅ |
| Parallel Gateway (AND) | ✅ |
| Event-based Gateway | ✅ |
| Intermediate Timer Catch Event | ✅ |
| Intermediate Message Catch Event | ✅ |
| Intermediate Signal Catch Event | ✅ |
| Boundary Timer / Message / Signal / Conditional Event (interrupting and non-interrupting) | ✅ |
| Boundary Error / Escalation Event | ✅ |
| Embedded Sub-Process (nested, with boundary events) | ✅ |
| Event Sub-Process (timer, message, signal, error, escalation, conditional; interrupting or not) | ✅ |
| Manual / Send / Receive Task; an untyped task passes straight through (as in Camunda 8; versions deployed before keep it as a user task) | ✅ |
| Intermediate Throw Event (none, signal, escalation, compensation, link) and Link Catch Event | ✅ |
| Intermediate Conditional Catch Event | ✅ |
| End Event: terminate, error, signal, escalation, compensation, message (worked by a job worker) | ✅ |
| Compensation (boundary + handler, throw with optional `activityRef`, nested into completed sub-processes) | ✅ |
| Multi-instance on any activity (every task type, embedded sub-process, call activity), parallel or sequential: `zeebe:loopCharacteristics` (inputCollection, inputElement, outputCollection, outputElement), loop cardinality, completion condition (`numberOf*Instances`), boundary events on the whole loop | ✅ |
| Variable scopes: local variables per sub-process, multi-instance iteration and activity (input mappings), propagation to the nearest scope that has the variable, output mappings evaluated in the activity scope, `SetVariables` with `local` | ✅ |
| Business Rule Task (DMN decision table) | ✅ |

Timers (every timer event) take `timeDuration` (ISO 8601, calendar units included: `P1M`, `P1DT2H`), `timeDate` (with offset, `@Zone`/`[Zone]` or local) and `timeCycle` (`R[n]/[start/]duration[/end]` or a Spring cron expression, `L`/`W`/`#` included), or a FEEL expression evaluated against the instance variables when armed; cycles repeat on non-interrupting boundary events and event sub-processes.

Conditional events are re-evaluated as soon as a variable of their instance changes (a database trigger notifies the engine, as Camunda 7 evaluates on each variable write; Camunda 8 has no conditional events). Known limit of the BPMN coverage: compensation runs its handlers all at once (it reaches into completed embedded sub-processes without a handler of their own, as in Camunda 8; there is no compensation event sub-process).
