import { apiClient } from './client';
import type { HistoricProcessInstance, HistoricActivityInstance } from '../types';

// The history API speaks Camunda 7 (ACTIVE, COMPLETED, EXTERNALLY_TERMINATED…);
// the pages work with BpmnGo's lowercase states.
const bpmngoState = (state: string): HistoricProcessInstance['state'] => {
  switch (state) {
    case 'EXTERNALLY_TERMINATED':
    case 'INTERNALLY_TERMINATED':
      return 'terminated';
    default:
      return (state ?? '').toLowerCase() as HistoricProcessInstance['state'];
  }
};

const withBpmnGoState = (h: HistoricProcessInstance): HistoricProcessInstance => ({ ...h, state: bpmngoState(h.state) });

export const listHistoricInstances = async (params?: {
  processDefinitionKey?: string;
  state?: string;
  startedAfter?: string;
  startedBefore?: string;
  firstResult?: number;
  maxResults?: number;
}): Promise<HistoricProcessInstance[]> => {
  const res = await apiClient.get('/engine-rest/history/process-instance', { params });
  return (res.data as HistoricProcessInstance[]).map(withBpmnGoState);
};

export const getHistoricInstance = async (id: string): Promise<HistoricProcessInstance> => {
  const res = await apiClient.get(`/engine-rest/history/process-instance/${id}`);
  return withBpmnGoState(res.data);
};

export const listHistoricActivities = async (
  processInstanceId: string
): Promise<HistoricActivityInstance[]> => {
  const res = await apiClient.get('/engine-rest/history/activity-instance', {
    params: { processInstanceId },
  });
  return res.data;
};
