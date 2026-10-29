import { apiClient } from './client';

export type Distribution = { type: 'fixed' | 'normal' | 'exponential' | 'uniform' | 'triangular'; value?: number; mean?: number; standardDeviation?: number; min?: number; max?: number; mode?: number };
export type CalendarShift = { weekdays?:number[];startMinute:number;endMinute:number;capacity?:number };
export type CalendarException = { date:string;shifts?:CalendarShift[] };
export type BusinessCalendar = { id:string;name:string;timezone:string;shifts:CalendarShift[];exceptions?:CalendarException[] };
export type ArrivalSegment = { startMinute:number;endMinute:number;ratePerHour:number };
export type ArrivalPattern = { type:'poisson'|'piecewise_poisson'|'scheduled'|'batch_poisson';ratePerHour?:number;segments?:ArrivalSegment[];scheduledMinutes?:number[];batchSize?:number };
export type SimulationResource = { id: string; name: string; capacity: number; costPerHour: number; calendarId?:string;skills?:string[] };
export type ActivityParameters = { processingTime: Distribution; waitingTime?: Distribution; resourceId?: string;resourceIds?:string[];requiredSkills?:string[];routingRule?:'fixed'|'least_queue'|'round_robin';priority?:number;valueAdded: boolean };
export type Scenario = {
  name: string; bpmnXml: string; arrivalRatePerHour: number; durationHours: number;
  maxArrivals?: number; replications: number; seed: number;startTime?:string;arrivalPattern?:ArrivalPattern;calendars?:BusinessCalendar[];resources: SimulationResource[];
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
export type StoredScenario = { id:string;name:string;bpmnXml:string;configuration:Omit<Scenario,'name'|'bpmnXml'>;currentVersion:number;createdAt:string;updatedAt:string };
export type ScenarioVersion = { id:string;scenarioId:string;version:number;name:string;createdAt:string };
export type ScenarioPackage = { format:'bpmngo-simulation-scenario/v1';exportedAt:string;scenario:Scenario };
export type CapacitySweepRequest = { resourceId:string;minimumCapacity:number;maximumCapacity:number;step:number;targetMeanCycleMinutes?:number;targetP95CycleMinutes?:number };
export type CapacitySweepResult = { scenarioName:string;resourceId:string;recommendedCapacity?:number;points:Array<{capacity:number;meanCycleMinutes:number;p95CycleMinutes:number;throughputPerHour:number;meanCost:number;utilization:number;meanQueueLength:number;maximumQueueSize:number;meetsTarget:boolean}> };
export type ExperimentDimension = { path:string;values:number[] };
export type ExperimentRequest = { dimensions:ExperimentDimension[];objective?:'mean_cycle'|'p95_cycle'|'cost'|'throughput';targetMeanCycleMinutes?:number;targetP95CycleMinutes?:number;maximumMeanCost?:number;minimumThroughput?:number };
export type ExperimentResult = { scenarioName:string;objective:string;recommendedPoint?:number;points:Array<{parameters:Record<string,number>;meanCycleMinutes:number;p95CycleMinutes:number;throughputPerHour:number;meanCost:number;meetsConstraints:boolean;error?:string}> };
export type SimulationRun = { id:string;scenarioId:string;status:'queued'|'running'|'completed'|'failed'|'cancelled';runType:'simulation'|'capacity_sweep'|'experiment';request?:CapacitySweepRequest|ExperimentRequest;progress:number;result?:SimulationResult|CapacitySweepResult|ExperimentResult;errorMessage?:string;startedAt:string;completedAt?:string };
export type StaffingResult = { minimumServers:number;points:Array<{servers:number;utilization:number;probabilityOfWait:number;meanWaitingHours:number;meanSystemHours:number;meetsTarget:boolean;stable:boolean}> };

export async function analyzeScenario(scenario: Scenario) { return (await apiClient.post<AnalyticResult>('/engine-rest/simulation/analyze', scenario)).data; }
export async function simulateScenario(scenario: Scenario) { return (await apiClient.post<SimulationResult>('/engine-rest/simulation/simulate', scenario)).data; }
export async function calculateStaffing(input:{arrivalRatePerHour:number;serviceTimeHours:number;targetWaitingHours:number;maxServers:number}) { return (await apiClient.post<StaffingResult>('/engine-rest/simulation/staffing',input)).data; }
export async function listScenarios() { return (await apiClient.get<StoredScenario[]>('/engine-rest/simulation/scenarios')).data; }
export async function createScenario(scenario:Scenario) { return (await apiClient.post<StoredScenario>('/engine-rest/simulation/scenarios',scenario)).data; }
export async function updateScenario(id:string,scenario:Scenario) { return (await apiClient.put<StoredScenario>(`/engine-rest/simulation/scenarios/${id}`,scenario)).data; }
export async function listScenarioVersions(id:string) { return (await apiClient.get<ScenarioVersion[]>(`/engine-rest/simulation/scenarios/${id}/versions`)).data; }
export async function restoreScenarioVersion(id:string,version:number) { return (await apiClient.post<StoredScenario>(`/engine-rest/simulation/scenarios/${id}/versions/${version}/restore`)).data; }
export async function exportScenario(id:string) { return (await apiClient.get<ScenarioPackage>(`/engine-rest/simulation/scenarios/${id}/export`)).data; }
export async function importScenario(input:ScenarioPackage) { return (await apiClient.post<StoredScenario>('/engine-rest/simulation/scenarios/import',input)).data; }
export async function runStoredScenario(id:string) { return (await apiClient.post<SimulationRun>(`/engine-rest/simulation/scenarios/${id}/runs`)).data; }
export async function runCapacitySweep(id:string,input:CapacitySweepRequest) { return (await apiClient.post<SimulationRun>(`/engine-rest/simulation/scenarios/${id}/capacity-sweeps`,input)).data; }
export async function runExperiment(id:string,input:ExperimentRequest) { return (await apiClient.post<SimulationRun>(`/engine-rest/simulation/scenarios/${id}/experiments`,input)).data; }
export async function getSimulationRun(id:string) { return (await apiClient.get<SimulationRun>(`/engine-rest/simulation/runs/${id}`)).data; }
export async function listSimulationRuns(id:string) { return (await apiClient.get<SimulationRun[]>(`/engine-rest/simulation/scenarios/${id}/runs`)).data; }
export async function cancelSimulationRun(id:string) { return (await apiClient.post<SimulationRun>(`/engine-rest/simulation/runs/${id}/cancel`)).data; }
export async function listIssues(id:string) { return (await apiClient.get<Issue[]>(`/engine-rest/simulation/scenarios/${id}/issues`)).data; }
export async function createIssue(id:string,issue:Partial<Issue>) { return (await apiClient.post<Issue>(`/engine-rest/simulation/scenarios/${id}/issues`,issue)).data; }
export async function updateIssue(issue:Issue) { return (await apiClient.put<Issue>(`/engine-rest/simulation/issues/${issue.id}`,issue)).data; }
