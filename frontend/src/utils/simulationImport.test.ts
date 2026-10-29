import { describe, expect, it } from 'vitest';
import { emptyScenario } from '../data/simulationExamples';
import { configureScenarioFromBpmn } from './simulationImport';

const bpmn = `<?xml version="1.0"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="order" name="Order fulfilment">
    <startEvent id="start"/><userTask id="review"/><exclusiveGateway id="decision"/>
    <serviceTask id="approve"/><serviceTask id="reject"/><endEvent id="end"/>
    <sequenceFlow id="s1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="s2" sourceRef="review" targetRef="decision"/>
    <sequenceFlow id="yes" sourceRef="decision" targetRef="approve"/>
    <sequenceFlow id="no" sourceRef="decision" targetRef="reject"/>
  </process>
</definitions>`;

describe('configureScenarioFromBpmn', () => {
  it('discovers activities and initializes exclusive branch probabilities', () => {
    const { scenario, summary } = configureScenarioFromBpmn(emptyScenario(), bpmn, 'orders.bpmn');
    expect(scenario.name).toBe('Order fulfilment');
    expect(Object.keys(scenario.activities)).toEqual(['review', 'approve', 'reject']);
    expect(scenario.flows.yes.probability).toBe(0.5);
    expect(scenario.flows.no.probability).toBe(0.5);
    expect(summary).toEqual({ processName: 'Order fulfilment', activityCount: 3, branchFlowCount: 2 });
  });

  it('preserves parameters for elements that still exist', () => {
    const first = configureScenarioFromBpmn(emptyScenario(), bpmn).scenario;
    first.activities.review.processingTime = { type: 'fixed', value: 42 };
    first.flows.yes.probability = 0.8;
    first.flows.no.probability = 0.2;
    const second = configureScenarioFromBpmn(first, bpmn).scenario;
    expect(second.activities.review.processingTime.value).toBe(42);
    expect(second.flows.yes.probability).toBe(0.8);
  });

  it('discovers nested activities, event races, boundaries and atomic calls', () => {
    const advanced = `<?xml version="1.0"?><definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="advanced">
      <startEvent id="start"/><subProcess id="embedded"><startEvent id="inner_start"/><userTask id="inner_work"/><endEvent id="inner_end"/></subProcess>
      <eventBasedGateway id="race"/><receiveTask id="reply"/><receiveTask id="timeout"/><callActivity id="external" calledElement="external-process"/><boundaryEvent id="failure" attachedToRef="external"/><endEvent id="end"/>
      <sequenceFlow id="r1" sourceRef="race" targetRef="reply"/><sequenceFlow id="r2" sourceRef="race" targetRef="timeout"/><sequenceFlow id="failure_flow" sourceRef="failure" targetRef="end"/>
    </process></definitions>`;
    const { scenario } = configureScenarioFromBpmn(emptyScenario(), advanced);
    expect(scenario.activities.embedded).toBeUndefined();
    expect(scenario.activities.inner_work).toBeDefined();
    expect(scenario.activities.external).toBeDefined();
    expect(scenario.flows.r1.probability).toBe(0.5);
    expect(scenario.flows.r2.probability).toBe(0.5);
    expect(scenario.flows.failure_flow.probability).toBe(0.1);
  });
});
