import { useState } from 'react';
import type { ActivityParameters, Distribution, Scenario, SimulationResult } from '../../api/simulation';
import { elementKey, modelElements, projectElementId, upsertProjectModel } from '../../utils/simulationProject';
import { SimulationDiagram, type DiagramMark } from './SimulationDiagram';

const num=(value:string)=>Number(value)||0;
const list=(value:string)=>value.split(',').map(s=>s.trim()).filter(Boolean);
const baseActivity:ActivityParameters={processingTime:{type:'fixed',value:0},waitingTime:{type:'fixed',value:0},valueAdded:false};

export function DistributionEditor({label,value,onChange}:{label:string;value:Distribution;onChange:(d:Distribution)=>void}) {
  value={...value,type:value.type||'fixed'};
  const fields=value.type==='fixed'?['value']:value.type==='normal'?['mean','standardDeviation']:value.type==='exponential'?['mean']:value.type==='uniform'?['min','max']:['min','mode','max'];
  return <fieldset className="border rounded-lg p-3 space-y-2"><legend className="text-xs font-medium px-1">{label} (minutes)</legend>
    <select aria-label={`${label} distribution`} className="input" value={value.type} onChange={e=>onChange({type:e.target.value as Distribution['type'],value:0,mean:1,standardDeviation:0,min:0,mode:1,max:2})}>{['fixed','normal','exponential','uniform','triangular'].map(type=><option key={type}>{type}</option>)}</select>
    {fields.map(field=><label key={field} className="block text-xs text-gray-600">{field}<input aria-label={`${label} ${field}`} className="input mt-1" type="number" min="0" step="any" value={value[field as keyof Distribution]??0} onChange={e=>onChange({...value,[field]:num(e.target.value)})}/></label>)}
  </fieldset>;
}

export function SimulationProject({scenario,onChange,onModeler,onSave,onWorkload,busy}:{scenario:Scenario;onChange:(s:Scenario)=>void;onModeler:(modelId?:string)=>void;onSave:()=>void;onWorkload:()=>void;busy:boolean}) {
  const [selectedModel,setSelectedModel]=useState(''),[selectedElement,setSelectedElement]=useState(''),[error,setError]=useState('');
  const models=scenario.models??[];
  const model=models.find(m=>m.id===selectedModel)??models[0];
  const elements=model?modelElements(model.bpmnXml):[];
  const selected=elements.find(e=>e.id===selectedElement);
  const key=model?elementKey(model.id,selectedElement):'';
  const activity=scenario.activities[key];
  const flow=scenario.flows[key];
  const patchActivity=(patch:Partial<ActivityParameters>)=>onChange({...scenario,activities:{...scenario.activities,[key]:{...(activity??baseActivity),...patch}}});
  const addFiles=async(files:FileList|null)=>{
    if(!files)return;try{let next=scenario;for(const file of Array.from(files))next=upsertProjectModel(next,await file.text(),file.name.replace(/\.(bpmn|xml)$/i,''));onChange(next);setSelectedModel(next.models?.at(-1)?.id??'');setSelectedElement('');setError('')}catch(e){setError((e as Error).message)}
  };
  const transfer=scenario.transfers?.find(t=>t.sourceModelId===model?.id&&t.sourceNodeId===selectedElement);
  const patchTransfer=(targetModelId:string)=>{
    if(!model)return;const remaining=(scenario.transfers??[]).filter(t=>t!==transfer);
    if(!targetModelId){onChange({...scenario,transfers:remaining});return}
    const target=models.find(m=>m.id===targetModelId)!;
    const first=modelElements(target.bpmnXml).find(e=>e.topLevel&&e.type==='startEvent');
    onChange({...scenario,transfers:[...remaining,{id:transfer?.id??projectElementId(),sourceModelId:model.id,sourceNodeId:selectedElement,targetModelId,targetNodeId:first?.id??'',delay:transfer?.delay??{type:'fixed',value:0},cost:transfer?.cost??0}]});
  };
  const updateTransfer=(patch:Partial<NonNullable<Scenario['transfers']>[number]>)=>onChange({...scenario,transfers:scenario.transfers?.map(t=>t===transfer?{...t,...patch}:t)});
  const removeModel=()=>{
    if(!model||!window.confirm(`Remove ${model.name} and its constraints and handoffs from this project?`))return;
    const remaining=models.filter(m=>m.id!==model.id),prefix=`${model.id}::`;
    onChange({...scenario,models:remaining,bpmnXml:remaining[0]?.bpmnXml??'',entryModelId:scenario.entryModelId===model.id?remaining[0]?.id:scenario.entryModelId,
      activities:Object.fromEntries(Object.entries(scenario.activities).filter(([id])=>!id.startsWith(prefix))),flows:Object.fromEntries(Object.entries(scenario.flows).filter(([id])=>!id.startsWith(prefix))),
      transfers:scenario.transfers?.filter(t=>t.sourceModelId!==model.id&&t.targetModelId!==model.id)});
    setSelectedElement('');
  };
  return <div className="space-y-4">
    <div className="bg-white border rounded-xl p-5 flex flex-wrap gap-4 items-end">
      <label className="flex-1 text-xs text-gray-600 min-w-48">Project name<input aria-label="Project name" className="input mt-1" value={scenario.name} onChange={e=>onChange({...scenario,name:e.target.value})}/></label>
      <button disabled={busy} onClick={onSave} className="px-4 py-2 border rounded-lg text-sm bg-white">Save project</button>
      <button disabled={!models.length} onClick={onWorkload} className="px-4 py-2 rounded-lg text-sm bg-blue-600 text-white disabled:opacity-50">Configure workload →</button>
    </div>
    {error&&<p role="alert" className="text-red-700">{error}</p>}
    <div className="grid xl:grid-cols-[220px_minmax(0,1fr)_310px] gap-4 items-start">
      <aside className="bg-white border rounded-xl p-4 space-y-3">
        <h2 className="font-semibold text-sm">Project models <span className="text-gray-400">{models.length}</span></h2>
        {models.map(m=><button key={m.id} onClick={()=>{setSelectedModel(m.id);setSelectedElement('')}} className={`w-full text-left rounded-lg p-3 text-sm ${model?.id===m.id?'bg-blue-50 text-blue-700':'hover:bg-gray-50'}`}>{m.name}{scenario.entryModelId===m.id&&<span className="block text-xs text-gray-500">Workload entry</span>}</button>)}
        <label className="block border rounded-lg p-2 text-center text-sm cursor-pointer">Upload BPMN<input aria-label="Upload project BPMN" type="file" accept=".bpmn,.xml" multiple className="hidden" onChange={e=>{void addFiles(e.target.files);e.target.value=''}}/></label>
        <button disabled={busy} className="w-full border rounded-lg p-2 text-sm" onClick={()=>onModeler()}>Create in modeler</button>
        <p className="text-xs text-gray-500">Models share the project workload and resource pools. Assign separate pools for independent teams, or the same pool for centralized capacity.</p>
      </aside>
      <div className="min-w-0 space-y-3">
        {model?<><div className="flex items-center justify-between gap-2"><input aria-label="Model name" value={model.name} className="input font-medium" onChange={e=>onChange({...scenario,models:models.map(m=>m.id===model.id?{...m,name:e.target.value}:m)})}/><button className="whitespace-nowrap text-sm text-blue-600" onClick={()=>onModeler(model.id)}>Edit BPMN</button><button className="text-sm text-red-600" onClick={removeModel}>Remove</button></div><SimulationDiagram key={model.id} xml={model.bpmnXml} onSelect={setSelectedElement} marks={selectedElement?[{id:selectedElement,kind:'current'}]:[]}/><p className="text-xs text-gray-500">Select an element to set its duration, waiting time, cost, resource constraints or a handoff to another model.</p></>:<div className="border-2 border-dashed rounded-xl p-16 text-center text-gray-500"><h2 className="font-medium text-gray-900">Build your simulation project</h2><p className="mt-2 text-sm">Save a project, then upload one or more BPMN files or create a model.</p></div>}
        {!!scenario.transfers?.length&&<div className="border rounded-xl bg-white p-4"><h3 className="font-medium text-sm mb-2">Connections between models</h3>{scenario.transfers.map(t=><button key={t.id} className="block text-left text-sm py-2 text-blue-700" onClick={()=>{setSelectedModel(t.sourceModelId);setSelectedElement(t.sourceNodeId)}}>{models.find(m=>m.id===t.sourceModelId)?.name} / {t.sourceNodeId} → {models.find(m=>m.id===t.targetModelId)?.name} / {t.targetNodeId}</button>)}</div>}
      </div>
      <aside className="bg-white border rounded-xl p-4 space-y-4">
        <h2 className="font-semibold text-sm">Element constraints</h2>
        <select aria-label="BPMN element" value={selectedElement} onChange={e=>setSelectedElement(e.target.value)} className="input"><option value="">Select on the diagram…</option>{elements.map(e=><option key={e.id} value={e.id}>{e.name} ({e.type})</option>)}</select>
        {selected&&<><div className="text-xs text-gray-500 break-all">{selected.id}</div>
          {selected.type==='sequenceFlow'?flow?<label className="block text-xs">Branch probability<input className="input mt-1" type="number" min="0" max="1" step="0.01" value={flow.probability} onChange={e=>onChange({...scenario,flows:{...scenario.flows,[key]:{probability:num(e.target.value)}}})}/></label>:<p className="text-sm text-gray-500">This sequence flow is deterministic. Configure branching at an exclusive, inclusive, or event-based gateway.</p>:
          selected.type==='subProcess'||selected.type.endsWith('Gateway')||selected.type==='startEvent'||selected.type==='endEvent'?<p className="text-sm text-gray-500">Select a task or waiting event to configure time and cost. Select an outgoing branch to configure its probability.</p>:<>
            <DistributionEditor label="Processing" value={activity?.processingTime??baseActivity.processingTime} onChange={processingTime=>patchActivity({processingTime})}/>
            <DistributionEditor label="Waiting" value={activity?.waitingTime??{type:'fixed',value:0}} onChange={waitingTime=>patchActivity({waitingTime})}/>
            <label className="block text-xs">Fixed cost per visit<input aria-label="Fixed cost per visit" className="input mt-1" type="number" min="0" value={activity?.fixedCost??0} onChange={e=>patchActivity({fixedCost:num(e.target.value)})}/></label>
            <label className="block text-xs">Resource pool<select aria-label="Resource pool" className="input mt-1" value={activity?.resourceId??''} onChange={e=>patchActivity({resourceId:e.target.value||undefined})}><option value="">No resource / eligible pools</option>{scenario.resources.map(r=><option key={r.id} value={r.id}>{r.name}</option>)}</select></label>
            <label className="block text-xs">Eligible pools (comma separated)<input className="input mt-1" value={activity?.resourceIds?.join(', ')??''} onChange={e=>patchActivity({resourceIds:list(e.target.value)})}/></label>
            <label className="block text-xs">Required skills<input className="input mt-1" value={activity?.requiredSkills?.join(', ')??''} onChange={e=>patchActivity({requiredSkills:list(e.target.value)})}/></label>
            <label className="block text-xs">Routing<select className="input mt-1" value={activity?.routingRule??'least_queue'} onChange={e=>patchActivity({routingRule:e.target.value as ActivityParameters['routingRule']})}><option value="least_queue">Least queue</option><option value="round_robin">Round robin</option><option value="fixed">Fixed</option></select></label>
            <label className="block text-xs">Priority<input className="input mt-1" type="number" value={activity?.priority??0} onChange={e=>patchActivity({priority:num(e.target.value)})}/></label>
            <label className="flex gap-2 text-xs"><input type="checkbox" checked={activity?.valueAdded??false} onChange={e=>patchActivity({valueAdded:e.target.checked})}/>Value-added work</label>
          </>}
          {selected.topLevel&&selected.type!=='sequenceFlow'&&!['subProcess','parallelGateway','inclusiveGateway'].includes(selected.type)&&models.length>1&&<div className="pt-4 border-t space-y-3"><h3 className="font-medium text-sm">Token handoff</h3><p className="text-xs text-gray-500">After this element, continue the same case in another model. This replaces its normal continuation.</p><select aria-label="Handoff target model" className="input" value={transfer?.targetModelId??''} onChange={e=>patchTransfer(e.target.value)}><option value="">Continue within this model</option>{models.filter(m=>m.id!==model.id).map(m=><option key={m.id} value={m.id}>{m.name}</option>)}</select>{transfer&&<><select aria-label="Handoff target element" className="input" value={transfer.targetNodeId} onChange={e=>updateTransfer({targetNodeId:e.target.value})}><option value="">Select target element</option>{modelElements(models.find(m=>m.id===transfer.targetModelId)?.bpmnXml??'').filter(e=>e.topLevel&&e.type!=='sequenceFlow').map(e=><option key={e.id} value={e.id}>{e.name}</option>)}</select><DistributionEditor label="Transit delay" value={transfer.delay} onChange={delay=>updateTransfer({delay})}/><label className="block text-xs">Handoff cost<input className="input mt-1" type="number" min="0" value={transfer.cost??0} onChange={e=>updateTransfer({cost:num(e.target.value)})}/></label></>}</div>}
        </>}
      </aside>
    </div>
  </div>;
}

export function SimulationReplay({result}:{result:SimulationResult}) {
  const snapshot=result.scenarioSnapshot;
  const [selectedModel,setSelectedModel]=useState(''),[caseId,setCaseId]=useState(0),[step,setStep]=useState(-1);
  const trace=result.metrics.trace??[],cases=Array.from(new Set(trace.map(e=>e.caseId))),events=trace.filter(e=>e.caseId===caseId),event=step>=0?events[Math.min(step,events.length-1)]:undefined;
  const models=snapshot?.models?.length?snapshot.models:snapshot?.bpmnXml?[{id:'',name:result.scenarioName,bpmnXml:snapshot.bpmnXml}]:[];
  const handoff=(id:string)=>snapshot?.transfers?.find(t=>id===`@transfer::${t.id}`);
  const eventModel=event?(handoff(event.elementId)?.sourceModelId??event.elementId.split('::')[0]):undefined;
  const model=(event?models.find(m=>m.id===eventModel):undefined)??models.find(m=>m.id===selectedModel)??models[0];
  if(!model)return <p className="p-4 text-sm text-gray-500">This older run has no model snapshot. Run the project again to capture its execution path.</p>;
  const prefix=model.id?`${model.id}::`:'';
  const local=(id:string)=>prefix?id.startsWith(prefix)?id.slice(prefix.length):undefined:id;
  const diagramElement=(id:string)=>{const transfer=handoff(id);return transfer?transfer.sourceModelId===model.id?transfer.sourceNodeId:undefined:local(id)};
  const label=(id:string)=>{
    const transfer=handoff(id);
    if(transfer)return `Handoff: ${models.find(m=>m.id===transfer.sourceModelId)?.name??transfer.sourceModelId} → ${models.find(m=>m.id===transfer.targetModelId)?.name??transfer.targetModelId}`;
    const owner=models.find(m=>m.id? id.startsWith(`${m.id}::`):true),elementId=owner?.id?id.slice(owner.id.length+2):id;
    return `${owner?.name??'Model'} / ${modelElements(owner?.bpmnXml??'').find(e=>e.id===elementId)?.name??elementId}`;
  };
  const marks:DiagramMark[]=[];
  const shown=event?events.slice(0,step+1):trace;
  if(event){shown.forEach(e=>{const id=local(e.elementId);if(id)marks.push({id,kind:'visited'});const flow=e.flowId&&local(e.flowId);if(flow)marks.push({id:flow,kind:'visited'})})}
  else{result.metrics.flowCounts?.forEach(f=>{const id=local(f.flowId);if(id)marks.push({id,kind:'visited',label:String(f.count)})})}
  const bottlenecks=[...(result.metrics.elements??[])].filter(e=>e.completed>0&&e.meanQueueMinutes>0).sort((a,b)=>b.meanQueueMinutes-a.meanQueueMinutes);
  bottlenecks.slice(0,5).forEach(e=>{const id=diagramElement(e.elementId);if(id)marks.push({id,kind:'bottleneck',label:`${e.meanQueueMinutes.toFixed(2)} min wait`})});
  if(event){const id=diagramElement(event.elementId);if(id)marks.push({id,kind:'current',label:`${event.kind} @ ${event.at.toFixed(2)} min`})}
  const download=()=>{const blob=new Blob([JSON.stringify({scenario:result.scenarioSnapshot,replication:1,trace,flowCounts:result.metrics.flowCounts,truncated:result.metrics.traceTruncated},null,2)],{type:'application/json'});const url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='simulation-execution-log.json';a.click();URL.revokeObjectURL(url)};
  return <section className="border rounded-xl bg-white p-5 space-y-4"><div className="flex justify-between flex-wrap gap-3"><div><h2 className="font-semibold">As-run paths and bottlenecks</h2><p className="text-xs text-gray-500">Path counts: first replication, all cases. Replay: first 50 cases, up to 20,000 events. Red: highest observed waits; purple: replay position.</p></div><button onClick={download} className="border rounded px-3 py-2 text-sm">Export execution log</button></div>
    <div className="flex gap-3 items-center flex-wrap"><select aria-label="Replay model" className="input max-w-64" value={model.id} onChange={e=>{setSelectedModel(e.target.value);setStep(-1)}}>{models.map(m=><option key={m.id} value={m.id}>{m.name}</option>)}</select><select aria-label="Replay case" className="input max-w-40" value={caseId} onChange={e=>{setCaseId(Number(e.target.value));setStep(0)}}>{cases.map(id=><option key={id} value={id}>Case {id+1}</option>)}</select><button onClick={()=>setStep(-1)} className="text-sm text-blue-600">All paths</button><button disabled={!events.length} onClick={()=>setStep(Math.max(0,step-1))} className="border rounded px-3 py-1">←</button><button disabled={!events.length||step>=events.length-1} onClick={()=>setStep(Math.min(events.length-1,step+1))} className="border rounded px-3 py-1">→</button><span className="text-xs text-gray-500">{step<0?'Overview':`${step+1} / ${events.length}`}</span></div>
    {!!events.length&&<input aria-label="Replay position" type="range" min="-1" max={events.length-1} value={step} onChange={e=>setStep(Number(e.target.value))} className="w-full"/>}
    {event&&<p className="text-sm">Case {caseId+1} · {event.at.toFixed(3)} min · {event.kind} · {label(event.elementId)}{event.resourceId?` · ${event.resourceId}`:''}</p>}
    <SimulationDiagram key={model.id} xml={model.bpmnXml} marks={marks}/>
    {result.metrics.traceTruncated&&<p className="text-xs text-amber-700">The replay log is sampled/capped. Aggregate metrics and flow counts cover all simulated cases.</p>}
    <h3 className="font-medium text-sm">Where cases wait</h3><div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="text-left text-gray-500"><th>Model / activity</th><th>Completed</th><th>Mean wait</th><th>Mean service</th></tr></thead><tbody>{bottlenecks.slice(0,10).map(e=><tr key={e.elementId} className="border-t"><td className="py-2"><button className="text-left text-blue-700" title={e.elementId} onClick={()=>{setSelectedModel(handoff(e.elementId)?.sourceModelId??e.elementId.split('::')[0]);setStep(-1)}}>{label(e.elementId)}</button></td><td>{e.completed}</td><td>{e.meanQueueMinutes.toFixed(2)} min</td><td>{e.meanServiceMinutes.toFixed(2)} min</td></tr>)}</tbody></table>{!bottlenecks.length&&<p className="text-sm text-gray-500 py-2">No waiting was observed in this run.</p>}</div>
  </section>;
}
