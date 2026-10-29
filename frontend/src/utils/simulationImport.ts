import type { ActivityParameters, Scenario } from '../api/simulation';

const activityTypes = new Set([
  'task', 'userTask', 'serviceTask', 'manualTask', 'businessRuleTask',
  'scriptTask', 'sendTask', 'receiveTask', 'callActivity', 'subProcess',
]);

const defaultActivity = (): ActivityParameters => ({
  processingTime: { type: 'fixed', value: 5 },
  waitingTime: { type: 'fixed', value: 0 },
  valueAdded: true,
});

export type ImportSummary = {
  processName?: string;
  activityCount: number;
  branchFlowCount: number;
};

export function configureScenarioFromBpmn(current: Scenario, bpmnXml: string, fallbackName?: string): { scenario: Scenario; summary: ImportSummary } {
  const document = new DOMParser().parseFromString(bpmnXml, 'application/xml');
  const parserError = document.querySelector('parsererror');
  if (parserError) throw new Error('The selected file is not valid XML.');

  const process = Array.from(document.getElementsByTagNameNS('*', 'process'))[0];
  if (!process) throw new Error('The selected file does not contain a BPMN process.');

  const elements = Array.from(process.getElementsByTagName('*'));
  const activities = { ...current.activities };
  const activityIDs = new Set<string>();
  for (const element of elements) {
    if (!activityTypes.has(element.localName)) continue;
    const id = element.getAttribute('id');
    if (!id) continue;
    activityIDs.add(id);
    activities[id] ??= defaultActivity();
  }
  for (const id of Object.keys(activities)) {
    if (!activityIDs.has(id)) delete activities[id];
  }

  const sequenceFlows = new Map<string, Element>();
  for (const element of elements) {
    if (element.localName === 'sequenceFlow') {
      const id = element.getAttribute('id');
      if (id) sequenceFlows.set(id, element);
    }
  }
  const flows = { ...current.flows };
  const configuredFlowIDs = new Set<string>();
  for (const gateway of elements.filter(element => element.localName === 'exclusiveGateway' || element.localName === 'inclusiveGateway')) {
    const gatewayID = gateway.getAttribute('id');
    if (!gatewayID) continue;
    const outgoing = Array.from(sequenceFlows.values()).filter(flow => flow.getAttribute('sourceRef') === gatewayID);
    if (outgoing.length <= 1) continue;
    const defaultProbability = gateway.localName === 'exclusiveGateway' ? 1 / outgoing.length : 0.5;
    for (const flow of outgoing) {
      const id = flow.getAttribute('id');
      if (!id) continue;
      configuredFlowIDs.add(id);
      flows[id] ??= { probability: defaultProbability };
    }
    if (gateway.localName === 'exclusiveGateway') {
      const ids = outgoing.map(flow => flow.getAttribute('id')).filter((id): id is string => !!id);
      const total = ids.reduce((sum, id) => sum + Math.max(0, flows[id].probability), 0);
      if (total > 0) {
        for (const id of ids) flows[id] = { probability: Math.max(0, flows[id].probability) / total };
      }
    }
  }
  for (const id of Object.keys(flows)) {
    if (!configuredFlowIDs.has(id)) delete flows[id];
  }

  const processName = process.getAttribute('name')?.trim() || undefined;
  return {
    scenario: {
      ...current,
      name: current.bpmnXml ? current.name : processName || fallbackName || current.name,
      bpmnXml,
      activities,
      flows,
    },
    summary: { processName, activityCount: activityIDs.size, branchFlowCount: configuredFlowIDs.size },
  };
}
