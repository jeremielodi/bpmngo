import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { AlertTriangle, BarChart3, Calculator, FlaskConical, Play, Save, Upload } from 'lucide-react';
import {
  analyzeScenario, calculateStaffing, createIssue, createScenario, listIssues, listScenarios, runStoredScenario, updateIssue,
  updateScenario, type AnalyticResult, type Scenario, type SimulationResult, type StaffingResult,
} from '../api/simulation';
import { helpdeskScenario } from '../data/helpdeskSimulation';
import { Card, CardHeader, Table, Thead, Th, Tr, Td } from '../components/ui';

type Tab = 'setup'|'results'|'staffing'|'issues';
const copyTemplate = () => structuredClone(helpdeskScenario);
const numeric = (value:string) => Number.isFinite(Number(value)) ? Number(value) : 0;
const durationMean = (d?: {type:string;value?:number;mean?:number;min?:number;max?:number;mode?:number}) => {
  if(!d)return 0;if(d.type==='fixed')return d.value??0;if(d.type==='uniform')return ((d.min??0)+(d.max??0))/2;
  if(d.type==='triangular')return ((d.min??0)+(d.mode??0)+(d.max??0))/3;return d.mean??0;
};

export default function SimulationLab() {
  const queryClient = useQueryClient();
  const [scenario,setScenario] = useState<Scenario>(copyTemplate);
  const [scenarioId,setScenarioId] = useState('');
  const [tab,setTab] = useState<Tab>('setup');
  const [analytic,setAnalytic] = useState<AnalyticResult>();
  const [result,setResult] = useState<SimulationResult>();
  const [busy,setBusy] = useState('');
  const [error,setError] = useState('');
  const [staffingInput,setStaffingInput] = useState({arrivalRatePerHour:1,serviceTimeHours:2,targetWaitingHours:2,maxServers:20});
  const [staffing,setStaffing] = useState<StaffingResult>();
  const [baseline,setBaseline] = useState<SimulationResult>();
  const {data:stored=[]} = useQuery({queryKey:['simulation-scenarios'],queryFn:listScenarios,retry:false});
  const activityRows = useMemo(()=>Object.entries(scenario.activities),[scenario.activities]);
  const flowRows = useMemo(()=>Object.entries(scenario.flows),[scenario.flows]);

  const execute = async (action:string, fn:()=>Promise<void>) => {
    setBusy(action);setError('');try{await fn()}catch(e:any){setError(e?.response?.data?.message ?? e?.message ?? 'Operation failed')}finally{setBusy('')}
  };
  const patchScenario = (patch:Partial<Scenario>) => setScenario(current=>({...current,...patch}));
  const patchActivity = (id:string,patch:Record<string,unknown>) => setScenario(current=>({...current,activities:{...current.activities,[id]:{...current.activities[id],...patch}}}));
  const setDistributionMean = (id:string,kind:'processingTime'|'waitingTime',value:number) => {
    const old = scenario.activities[id][kind] ?? {type:'fixed' as const,value:0};
    const next = old.type === 'fixed' ? {...old,value}
      : old.type === 'uniform' ? {...old,min:0,max:value*2}
      : old.type === 'triangular' ? {...old,min:0,mode:value,max:value*2}
      : {...old,mean:value};
    patchActivity(id,{[kind]:next});
  };
  const applyExercise3 = () => setScenario(current=>{
    const next=structuredClone(current);next.name='Helpdesk — exercise 3 (4 requests/hour)';next.arrivalRatePerHour=4;
    for(const id of ['Activity_0rt6ids','Activity_1npzcct','Activity_1kwn3zv','Activity_1pt3bpc']){
      if(next.activities[id])next.activities[id].waitingTime={type:'fixed',value:0};
    }
    return next;
  });
  const save = async () => {
    const row = scenarioId ? await updateScenario(scenarioId,scenario) : await createScenario(scenario);
    setScenarioId(row.id);await queryClient.invalidateQueries({queryKey:['simulation-scenarios']});return row.id;
  };
  const loadStored = (id:string) => {
    const row=stored.find(item=>item.id===id);if(!row)return;
    setScenarioId(id);setScenario({...row.configuration,name:row.name,bpmnXml:row.bpmnXml});setAnalytic(undefined);setResult(undefined);
  };

  return <div className="p-6 space-y-5">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div><h1 className="text-xl font-bold text-gray-900 flex items-center gap-2"><FlaskConical className="w-5 h-5 text-blue-600"/>Simulation lab</h1><p className="text-sm text-gray-500 mt-0.5">Analyze, staff, simulate, and redesign a BPMN process without executing production instances.</p></div>
      <div className="flex gap-2">
        <Link to="/modeler" className="px-3 py-2 text-sm border rounded-lg bg-white">Open modeler</Link>
        <select value={scenarioId} onChange={e=>loadStored(e.target.value)} className="border border-gray-200 rounded-lg px-3 py-2 text-sm bg-white"><option value="">Unsaved scenario</option>{stored.map(s=><option key={s.id} value={s.id}>{s.name}</option>)}</select>
        <button onClick={()=>{setScenario(copyTemplate());setScenarioId('');setResult(undefined);setAnalytic(undefined)}} className="px-3 py-2 text-sm border rounded-lg bg-white">Load helpdesk template</button>
        <button onClick={applyExercise3} className="px-3 py-2 text-sm border rounded-lg bg-white">Exercise 3 preset</button>
      </div>
    </div>
    <div className="flex border-b border-gray-200">{(['setup','results','staffing','issues'] as Tab[]).map(value=><button key={value} onClick={()=>setTab(value)} className={`px-4 py-2 text-sm capitalize border-b-2 ${tab===value?'border-blue-600 text-blue-600':'border-transparent text-gray-500'}`}>{value}</button>)}</div>
    {error&&<div className="rounded-lg bg-red-50 border border-red-200 text-red-700 text-sm px-4 py-3">{error}</div>}

    {tab==='setup'&&<div className="space-y-5">
      <Card><CardHeader title="Experiment" subtitle="Times are expressed in minutes; arrival rate is cases per hour."/><div className="p-6 pt-2 grid sm:grid-cols-2 lg:grid-cols-5 gap-4">
        <Field label="Name"><input value={scenario.name} onChange={e=>patchScenario({name:e.target.value})} className="input"/></Field>
        <NumberField label="Arrival rate / hour" value={scenario.arrivalRatePerHour} onChange={v=>patchScenario({arrivalRatePerHour:v})}/>
        <NumberField label="Duration (hours)" value={scenario.durationHours} onChange={v=>patchScenario({durationHours:v})}/>
        <NumberField label="Replications" value={scenario.replications} onChange={v=>patchScenario({replications:Math.max(1,v)})}/>
        <NumberField label="Random seed" value={scenario.seed} onChange={v=>patchScenario({seed:v})}/>
      </div></Card>
      <Card><CardHeader title="BPMN source" subtitle="Import any BPMN 2.0 XML file; element IDs connect the model to parameters."/><div className="p-6 pt-2 space-y-3"><label className="inline-flex items-center gap-2 px-3 py-2 border rounded-lg text-sm cursor-pointer bg-white"><Upload className="w-4 h-4"/>Import .bpmn<input type="file" accept=".bpmn,.xml" className="hidden" onChange={async e=>{const f=e.target.files?.[0];if(f)patchScenario({bpmnXml:await f.text()})}}/></label><textarea value={scenario.bpmnXml} onChange={e=>patchScenario({bpmnXml:e.target.value})} className="w-full h-40 font-mono text-xs border border-gray-200 rounded-lg p-3"/></div></Card>
      <Card><CardHeader title="Resources" subtitle="Capacity controls queues; hourly cost determines cost per execution."/><Table><Thead><tr><Th>ID</Th><Th>Name</Th><Th>Capacity</Th><Th>Cost / hour</Th><Th></Th></tr></Thead><tbody>{scenario.resources.map((r,index)=><Tr key={r.id}><Td>{r.id}</Td><Td><input value={r.name} onChange={e=>patchScenario({resources:scenario.resources.map((x,i)=>i===index?{...x,name:e.target.value}:x)})} className="table-input"/></Td><Td><input type="number" value={r.capacity} onChange={e=>patchScenario({resources:scenario.resources.map((x,i)=>i===index?{...x,capacity:numeric(e.target.value)}:x)})} className="table-input w-20"/></Td><Td><input type="number" value={r.costPerHour} onChange={e=>patchScenario({resources:scenario.resources.map((x,i)=>i===index?{...x,costPerHour:numeric(e.target.value)}:x)})} className="table-input w-24"/></Td><Td><button onClick={()=>patchScenario({resources:scenario.resources.filter((_,i)=>i!==index)})} className="text-xs text-red-600">Remove</button></Td></Tr>)}</tbody></Table><div className="p-4 border-t"><button onClick={()=>{const id=window.prompt('Resource ID');if(id&&!scenario.resources.some(r=>r.id===id))patchScenario({resources:[...scenario.resources,{id,name:id,capacity:1,costPerHour:0}]})}} className="text-sm text-blue-600">+ Add resource</button></div></Card>
      <Card><CardHeader title="Activity parameters" subtitle="Explicit wait models delays such as handoffs or client response; resource queues are calculated separately."/><div className="overflow-x-auto"><Table><Thead><tr><Th>BPMN element ID</Th><Th>Distribution</Th><Th>Processing mean</Th><Th>Std dev</Th><Th>Explicit wait</Th><Th>Resource</Th><Th>Value added</Th><Th></Th></tr></Thead><tbody>{activityRows.map(([id,a])=><Tr key={id}><Td><code className="text-xs">{id}</code></Td><Td><select value={a.processingTime.type} onChange={e=>patchActivity(id,{processingTime:{type:e.target.value,value:durationMean(a.processingTime),mean:durationMean(a.processingTime),standardDeviation:a.processingTime.standardDeviation??0}})} className="table-input"><option value="fixed">Fixed</option><option value="normal">Normal</option><option value="exponential">Exponential</option><option value="uniform">Uniform</option><option value="triangular">Triangular</option></select></Td><Td><input type="number" value={durationMean(a.processingTime)} onChange={e=>setDistributionMean(id,'processingTime',numeric(e.target.value))} className="table-input w-24"/></Td><Td><input type="number" disabled={a.processingTime.type!=='normal'} value={a.processingTime.standardDeviation??0} onChange={e=>patchActivity(id,{processingTime:{...a.processingTime,standardDeviation:numeric(e.target.value)}})} className="table-input w-20 disabled:bg-gray-100"/></Td><Td><input type="number" value={durationMean(a.waitingTime)} onChange={e=>setDistributionMean(id,'waitingTime',numeric(e.target.value))} className="table-input w-24"/></Td><Td><select value={a.resourceId??''} onChange={e=>patchActivity(id,{resourceId:e.target.value})} className="table-input"><option value="">None</option>{scenario.resources.map(r=><option key={r.id} value={r.id}>{r.name}</option>)}</select></Td><Td><input type="checkbox" checked={a.valueAdded} onChange={e=>patchActivity(id,{valueAdded:e.target.checked})}/></Td><Td><button onClick={()=>setScenario(current=>{const activities={...current.activities};delete activities[id];return {...current,activities}})} className="text-xs text-red-600">Remove</button></Td></Tr>)}</tbody></Table></div><div className="p-4 border-t"><button onClick={()=>{const id=window.prompt('BPMN activity element ID');if(id&&!scenario.activities[id])patchScenario({activities:{...scenario.activities,[id]:{processingTime:{type:'fixed',value:0},waitingTime:{type:'fixed',value:0},valueAdded:true}}})}} className="text-sm text-blue-600">+ Add activity</button></div></Card>
      <Card><CardHeader title="Branch probabilities" subtitle="Every gateway's outgoing probabilities must total 100%."/><div className="p-6 pt-2 grid sm:grid-cols-2 lg:grid-cols-4 gap-3">{flowRows.map(([id,p])=><div key={id} className="flex gap-1 items-end"><Field label={id}><input type="number" step="0.01" min="0" max="1" value={p.probability} onChange={e=>patchScenario({flows:{...scenario.flows,[id]:{probability:numeric(e.target.value)}}})} className="input"/></Field><button onClick={()=>setScenario(current=>{const flows={...current.flows};delete flows[id];return {...current,flows}})} className="px-2 py-2 text-xs text-red-600">×</button></div>)}</div><div className="px-6 pb-4"><button onClick={()=>{const id=window.prompt('BPMN sequence-flow ID');if(id&&!scenario.flows[id])patchScenario({flows:{...scenario.flows,[id]:{probability:0}}})}} className="text-sm text-blue-600">+ Add flow probability</button></div></Card>
      <div className="flex justify-end gap-2"><button disabled={!!busy} onClick={()=>execute('analyze',async()=>{setAnalytic(await analyzeScenario(scenario));setTab('results')})} className="px-4 py-2 border rounded-lg text-sm bg-white"><BarChart3 className="inline w-4 h-4 mr-2"/>Analyze</button><button disabled={!!busy} onClick={()=>execute('save',async()=>{await save()})} className="px-4 py-2 border rounded-lg text-sm bg-white"><Save className="inline w-4 h-4 mr-2"/>Save</button><button disabled={!!busy} onClick={()=>execute('run',async()=>{const id=await save();const run=await runStoredScenario(id);if(run.result)setResult(run.result);setTab('results')})} className="px-4 py-2 rounded-lg text-sm bg-blue-600 text-white"><Play className="inline w-4 h-4 mr-2"/>{busy==='run'?'Running…':'Save & run'}</button></div>
    </div>}

    {tab==='results'&&<><Results analytic={result?.analytic??analytic} result={result} baseline={baseline}/>{result&&<div className="flex justify-end gap-2"><button onClick={()=>setBaseline(result)} className="px-3 py-2 text-sm border rounded-lg bg-white">Use as as-is baseline</button><button onClick={()=>{setScenario(current=>({...structuredClone(current),name:`${current.name} — to-be`}));setScenarioId('');setResult(undefined);setTab('setup')}} className="px-3 py-2 text-sm bg-blue-600 text-white rounded-lg">Duplicate as to-be</button></div>}</>}
    {tab==='staffing'&&<div className="grid lg:grid-cols-3 gap-5"><Card className="lg:col-span-1"><CardHeader title="Erlang-C staffing" subtitle="M/M/c estimate for a shared work queue."/><div className="p-6 pt-2 space-y-4"><NumberField label="Arrival rate / hour" value={staffingInput.arrivalRatePerHour} onChange={v=>setStaffingInput({...staffingInput,arrivalRatePerHour:v})}/><NumberField label="Mean service time (hours)" value={staffingInput.serviceTimeHours} onChange={v=>setStaffingInput({...staffingInput,serviceTimeHours:v})}/><NumberField label="Target mean wait (hours)" value={staffingInput.targetWaitingHours} onChange={v=>setStaffingInput({...staffingInput,targetWaitingHours:v})}/><NumberField label="Maximum servers" value={staffingInput.maxServers} onChange={v=>setStaffingInput({...staffingInput,maxServers:v})}/><button onClick={()=>execute('staffing',async()=>setStaffing(await calculateStaffing(staffingInput)))} className="w-full px-4 py-2 rounded-lg bg-blue-600 text-white text-sm"><Calculator className="inline w-4 h-4 mr-2"/>Calculate minimum</button></div></Card><Card className="lg:col-span-2"><CardHeader title={staffing?`Minimum: ${staffing.minimumServers} servers`:'Staffing curve'} subtitle="Unstable configurations have utilization ≥ 100%."/>{staffing&&<Table><Thead><tr><Th>Servers</Th><Th>Utilization</Th><Th>P(wait)</Th><Th>Mean wait</Th><Th>Target</Th></tr></Thead><tbody>{staffing.points.map(p=><Tr key={p.servers}><Td>{p.servers}</Td><Td>{(p.utilization*100).toFixed(1)}%</Td><Td>{p.stable?`${(p.probabilityOfWait*100).toFixed(1)}%`:'—'}</Td><Td>{p.stable?`${p.meanWaitingHours.toFixed(2)} h`:'Unstable'}</Td><Td>{p.meetsTarget?'✓':'—'}</Td></Tr>)}</tbody></Table>}</Card></div>}
    {tab==='issues'&&<Issues result={result} scenarioId={scenarioId}/>}
  </div>;
}

function Results({analytic,result,baseline}:{analytic?:AnalyticResult;result?:SimulationResult;baseline?:SimulationResult}) {
  if(!analytic)return <Empty text="Run an analysis or simulation to see the results."/>;
  const metrics=result?.metrics;
  return <div className="space-y-5"><div className="grid sm:grid-cols-2 lg:grid-cols-5 gap-4"><KPI label="Expected cycle" value={`${analytic.cycleTimeMinutes.toFixed(1)} min`}/><KPI label="Processing" value={`${analytic.processingMinutes.toFixed(1)} min`}/><KPI label="Waiting" value={`${analytic.waitingMinutes.toFixed(1)} min`}/><KPI label="Cycle efficiency" value={`${(analytic.cycleTimeEfficiency*100).toFixed(1)}%`}/><KPI label="Cost / case" value={`€${analytic.costPerExecution.toFixed(2)}`}/></div>{metrics&&<>{baseline&&<Card><CardHeader title="As-is vs to-be" subtitle="Change relative to the selected baseline; negative cycle time and cost are improvements."/><div className="p-6 pt-2 grid sm:grid-cols-3 gap-4"><KPI label="Mean-cycle change" value={delta(metrics.meanCycleMinutes,baseline.metrics.meanCycleMinutes,' min')}/><KPI label="Cost change" value={delta(metrics.meanCost,baseline.metrics.meanCost,' €')}/><KPI label="Throughput change" value={delta(metrics.throughputPerHour,baseline.metrics.throughputPerHour,' / h')}/></div></Card>}<Card><CardHeader title="Simulation KPIs" subtitle={`${metrics.completed} completed cases; confidence intervals use independent replications.`}/><div className="p-6 pt-2 grid sm:grid-cols-2 lg:grid-cols-4 gap-4"><KPI label="Mean cycle" value={`${metrics.meanCycleMinutes.toFixed(1)} min`}/><KPI label="p95 cycle" value={`${metrics.p95CycleMinutes.toFixed(1)} min`}/><KPI label="Throughput" value={`${metrics.throughputPerHour.toFixed(2)} / h`}/><KPI label="Mean cost" value={`€${metrics.meanCost.toFixed(2)}`}/></div></Card><Card><CardHeader title="Resource pressure" subtitle="Utilization above 85% is flagged as a capacity risk."/><Table><Thead><tr><Th>Resource</Th><Th>Capacity</Th><Th>Utilization</Th><Th>Mean queue</Th><Th>Maximum queue</Th></tr></Thead><tbody>{metrics.resources.map(r=><Tr key={r.resourceId}><Td>{r.name}</Td><Td>{r.capacity}</Td><Td><span className={r.utilization>=.85?'text-red-600 font-semibold':''}>{(r.utilization*100).toFixed(1)}%</span></Td><Td>{r.meanQueueLength.toFixed(2)}</Td><Td>{r.maximumQueueSize}</Td></Tr>)}</tbody></Table></Card></>}</div>;
}
function Issues({result,scenarioId}:{result?:SimulationResult;scenarioId:string}) { const queryClient=useQueryClient();const {data:persisted=[]}=useQuery({queryKey:['simulation-issues',scenarioId],queryFn:()=>listIssues(scenarioId),enabled:!!scenarioId});const shown=persisted.length?persisted:(result?.issues??[]);const [title,setTitle]=useState('');if(!result&&!scenarioId)return <Empty text="Run the simulation to generate evidence-based issues and redesign recommendations."/>;return <div className="space-y-5"><Card><CardHeader title="Issue register" subtitle="Detected evidence is persisted; status can be managed as the redesign progresses."/><div className="divide-y">{shown.length===0?<p className="p-6 text-sm text-gray-500">No configured threshold was exceeded.</p>:shown.map(i=><div key={i.id??i.code} className="p-5 flex gap-3"><AlertTriangle className={`w-5 h-5 flex-none ${i.severity==='high'?'text-red-500':'text-amber-500'}`}/><div className="flex-1"><div className="flex justify-between gap-3"><p className="font-medium text-sm">{i.title}</p>{i.id&&<select value={i.status??'open'} onChange={async e=>{await updateIssue({...i,status:e.target.value});await queryClient.invalidateQueries({queryKey:['simulation-issues',scenarioId]})}} className="table-input"><option value="open">Open</option><option value="accepted">Accepted</option><option value="resolved">Resolved</option><option value="dismissed">Dismissed</option></select>}</div><p className="text-sm text-gray-600 mt-1">{i.evidence}</p><p className="text-xs text-blue-700 mt-2">{i.suggestion}</p></div></div>)}</div>{scenarioId&&<form className="p-4 border-t flex gap-2" onSubmit={async e=>{e.preventDefault();if(!title.trim())return;await createIssue(scenarioId,{title,code:'MANUAL',evidence:'Added by analyst',category:'other',severity:'medium',suggestion:'Review during redesign',status:'open'});setTitle('');await queryClient.invalidateQueries({queryKey:['simulation-issues',scenarioId]})}}><input value={title} onChange={e=>setTitle(e.target.value)} placeholder="Add an observed issue…" className="input"/><button className="px-3 py-2 bg-blue-600 text-white text-sm rounded-lg">Add</button></form>}</Card>{result&&<Card><CardHeader title="Redesign heuristics" subtitle="Candidates to test as a separate to-be scenario, not unverified prescriptions."/><div className="grid md:grid-cols-2 gap-4 p-6 pt-2">{result.recommendedDesign.map(r=><div key={r.title} className="border rounded-lg p-4"><span className="text-xs uppercase tracking-wide text-blue-600">{r.heuristic}</span><h3 className="font-semibold text-sm mt-1">{r.title}</h3><p className="text-sm text-gray-600 mt-2">{r.justification}</p></div>)}</div></Card>}</div>}
function delta(current:number,baseline:number,suffix:string){const value=current-baseline;return `${value>=0?'+':''}${value.toFixed(2)}${suffix}`}
function Field({label,children}:{label:string;children:React.ReactNode}){return <label className="block"><span className="block text-xs font-medium text-gray-600 mb-1">{label}</span>{children}</label>}
function NumberField({label,value,onChange}:{label:string;value:number;onChange:(v:number)=>void}){return <Field label={label}><input type="number" value={value} onChange={e=>onChange(numeric(e.target.value))} className="input"/></Field>}
function KPI({label,value}:{label:string;value:string}){return <div className="bg-white border border-gray-200 rounded-xl p-4"><p className="text-xl font-bold text-gray-900">{value}</p><p className="text-xs text-gray-500 mt-1">{label}</p></div>}
function Empty({text}:{text:string}){return <Card className="p-12 text-center text-sm text-gray-500">{text}</Card>}
