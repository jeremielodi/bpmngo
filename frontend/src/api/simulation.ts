import { apiClient } from './client';

export type Distribution = { type: 'fixed' | 'normal' | 'exponential' | 'uniform' | 'triangular'; value?: number; mean?: number; standardDeviation?: number; min?: number; max?: number; mode?: number };
export type SimulationResource = { id: string; name: string; capacity: number; costPerHour: number };
export type ActivityParameters = { processingTime: Distribution; waitingTime?: Distribution; resourceId?: string; valueAdded: boolean };
export type Scenario = {
  name: string; bpmnXml: string; arrivalRatePerHour: number; durationHours: number;
  maxArrivals?: number; replications: number; seed: number; resources: SimulationResource[];
  activities: Record<string, ActivityParameters>; flows: Record<string, { probability: number }>;
};
export type AnalyticResult = { processingMinutes: number; waitingMinutes: number; cycleTimeMinutes: number; valueAddedMinutes: number; cycleTimeEfficiency: number; costPerExecution: number; elements: Array<{ elementId: string; elementName: string; expectedVisits: number; processingMinutes: number; explicitWaitMinutes: number; expectedCost: number }> };
export type ResourceMetrics = { resourceId: string; name: string; capacity: number; utilization: number; meanQueueLength: number; maximumQueueSize: number; busyMinutes: number };
export type Issue = { id?: string; code: string; elementId?: string; title: string; evidence: string; category: string; severity: 'low'|'medium'|'high'; metric?: number; suggestion: string; status?: string };
export type SimulationResult = {
  scenarioName: string; replications: number; analytic: AnalyticResult;
  metrics: { arrived: number; completed: number; throughputPerHour: number; meanCycleMinutes: number; medianCycleMinutes: number; p90CycleMinutes: number; p95CycleMinutes: number; meanProcessingMinutes: number; meanWaitingMinutes: number; cycleTimeEfficiency: number; meanCost: number; resources: ResourceMetrics[]; elements: Array<{ elementId:string;elementName:string;completed:number;meanQueueMinutes:number;meanServiceMinutes:number }> };
  cycleTime95: { mean:number;lower:number;upper:number }; cost95: { mean:number;lower:number;upper:number }; throughput95: { mean:number;lower:number;upper:number };
  issues: Issue[]; recommendedDesign: Array<{ heuristic:string;title:string;justification:string;addressesIssues:string[] }>;
};
export type StoredScenario = { id:string;name:string;bpmnXml:string;configuration:Omit<Scenario,'name'|'bpmnXml'>;createdAt:string;updatedAt:string };
export type SimulationRun = { id:string;scenarioId:string;status:string;result?:SimulationResult;errorMessage?:string;startedAt:string;completedAt?:string };
export type StaffingResult = { minimumServers:number;points:Array<{servers:number;utilization:number;probabilityOfWait:number;meanWaitingHours:number;meanSystemHours:number;meetsTarget:boolean;stable:boolean}> };

export async function analyzeScenario(scenario: Scenario) { return (await apiClient.post<AnalyticResult>('/engine-rest/simulation/analyze', scenario)).data; }
export async function simulateScenario(scenario: Scenario) { return (await apiClient.post<SimulationResult>('/engine-rest/simulation/simulate', scenario)).data; }
export async function calculateStaffing(input:{arrivalRatePerHour:number;serviceTimeHours:number;targetWaitingHours:number;maxServers:number}) { return (await apiClient.post<StaffingResult>('/engine-rest/simulation/staffing',input)).data; }
export async function listScenarios() { return (await apiClient.get<StoredScenario[]>('/engine-rest/simulation/scenarios')).data; }
export async function createScenario(scenario:Scenario) { return (await apiClient.post<StoredScenario>('/engine-rest/simulation/scenarios',scenario)).data; }
export async function updateScenario(id:string,scenario:Scenario) { return (await apiClient.put<StoredScenario>(`/engine-rest/simulation/scenarios/${id}`,scenario)).data; }
export async function runStoredScenario(id:string) { return (await apiClient.post<SimulationRun>(`/engine-rest/simulation/scenarios/${id}/runs`)).data; }
export async function listIssues(id:string) { return (await apiClient.get<Issue[]>(`/engine-rest/simulation/scenarios/${id}/issues`)).data; }
export async function createIssue(id:string,issue:Partial<Issue>) { return (await apiClient.post<Issue>(`/engine-rest/simulation/scenarios/${id}/issues`,issue)).data; }
export async function updateIssue(issue:Issue) { return (await apiClient.put<Issue>(`/engine-rest/simulation/issues/${issue.id}`,issue)).data; }
