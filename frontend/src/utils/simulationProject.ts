import type { Scenario } from '../api/simulation';
import { configureScenarioFromBpmn } from './simulationImport';

export const elementKey = (modelId:string,elementId:string) => `${modelId}::${elementId}`;
export const projectElementId = ():string => {
  if(typeof crypto.randomUUID==='function')return crypto.randomUUID();
  return 'model-'+Array.from(crypto.getRandomValues(new Uint8Array(16)),v=>v.toString(16).padStart(2,'0')).join('');
};

export function asProject(s:Scenario):Scenario {
  if(s.models?.length || !s.bpmnXml)return {...s,models:s.models??[],transfers:s.transfers??[]};
  return {...s,models:[{id:'main',name:s.name,bpmnXml:s.bpmnXml}],entryModelId:'main',transfers:[],
    activities:Object.fromEntries(Object.entries(s.activities).map(([id,a])=>[elementKey('main',id),a])),
    flows:Object.fromEntries(Object.entries(s.flows).map(([id,f])=>[elementKey('main',id),f]))};
}

export function upsertProjectModel(project:Scenario,xml:string,name:string,id:string=projectElementId()):Scenario {
  const s=asProject(project),prefix=`${id}::`;
  const local={...s,bpmnXml:s.models?.find(m=>m.id===id)?.bpmnXml??'',
    activities:Object.fromEntries(Object.entries(s.activities).filter(([key])=>key.startsWith(prefix)).map(([key,a])=>[key.slice(prefix.length),a])),
    flows:Object.fromEntries(Object.entries(s.flows).filter(([key])=>key.startsWith(prefix)).map(([key,f])=>[key.slice(prefix.length),f]))};
  const imported=configureScenarioFromBpmn(local,xml,name);
  // Waiting events can be configured on the diagram too; preserve their
  // assumptions when the modeler saves a new revision of the same elements.
  for(const element of modelElements(xml)){
    if(['intermediateCatchEvent','intermediateThrowEvent','boundaryEvent'].includes(element.type)&&local.activities[element.id]){
      imported.scenario.activities[element.id]=local.activities[element.id];
    }
  }
  const model={id,name:s.models?.find(m=>m.id===id)?.name??imported.summary.processName??name,bpmnXml:xml};
  const models=s.models?.some(m=>m.id===id)?s.models.map(m=>m.id===id?model:m):[...(s.models??[]),model];
  return {...s,models,bpmnXml:models[0]?.bpmnXml??'',entryModelId:s.entryModelId??models[0]?.id,
    activities:{...Object.fromEntries(Object.entries(s.activities).filter(([key])=>!key.startsWith(prefix))),...Object.fromEntries(Object.entries(imported.scenario.activities).map(([key,a])=>[prefix+key,a]))},
    flows:{...Object.fromEntries(Object.entries(s.flows).filter(([key])=>!key.startsWith(prefix))),...Object.fromEntries(Object.entries(imported.scenario.flows).map(([key,f])=>[prefix+key,f]))}};
}

export function modelElements(xml:string) {
  const doc=new DOMParser().parseFromString(xml,'application/xml');
  const process=doc.getElementsByTagNameNS('*','process')[0];
  if(!process)return [];
  return Array.from(process.getElementsByTagName('*')).filter(e=>e.hasAttribute('id') && (/Task$|task$|Event$|Gateway$|callActivity|subProcess|sequenceFlow/.test(e.localName))).map(e=>({id:e.getAttribute('id')!,name:e.getAttribute('name')||e.getAttribute('id')!,type:e.localName,topLevel:e.parentElement===process}));
}

// Provide a readable fallback diagram for semantic-only BPMN imports. Existing
// BPMN DI, including user positioning and nested scopes, is preserved.
export function withDiagram(xml:string):string {
  const doc=new DOMParser().parseFromString(xml,'application/xml');
  if(doc.getElementsByTagNameNS('*','BPMNDiagram').length)return xml;
  const process=doc.getElementsByTagNameNS('*','process')[0];if(!process)return xml;
  const di='http://www.omg.org/spec/BPMN/20100524/DI',dc='http://www.omg.org/spec/DD/20100524/DC',dd='http://www.omg.org/spec/DD/20100524/DI';
  const add=(ns:string,tag:string,attrs:Record<string,string|number>,parent:Element)=>{const e=doc.createElementNS(ns,tag);Object.entries(attrs).forEach(([key,value])=>e.setAttribute(key,String(value)));parent.appendChild(e);return e};
  const diagram=add(di,'bpmndi:BPMNDiagram',{id:'simulation_diagram'},doc.documentElement);
  const plane=add(di,'bpmndi:BPMNPlane',{id:'simulation_plane',bpmnElement:process.getAttribute('id')??''},diagram);
  const positions=new Map<string,{x:number;y:number;w:number;h:number}>();
  const nodes=modelElements(xml).filter(e=>e.topLevel && e.type!=='sequenceFlow');
  nodes.forEach((node,i)=>{
    const event=node.type.endsWith('Event'),gateway=node.type.endsWith('Gateway');
    const w=event?36:gateway?50:120,h=event?36:gateway?50:80;
    const x=80+(i%5)*220,y=100+Math.floor(i/5)*180;
    positions.set(node.id,{x,y,w,h});const shape=add(di,'bpmndi:BPMNShape',{id:`${node.id}_sim_di`,bpmnElement:node.id},plane);
    add(dc,'dc:Bounds',{x,y,width:w,height:h},shape);
  });
  Array.from(process.children).filter(e=>e.localName==='sequenceFlow').forEach(flow=>{
    const a=positions.get(flow.getAttribute('sourceRef')??''),b=positions.get(flow.getAttribute('targetRef')??'');if(!a||!b)return;
    const edge=add(di,'bpmndi:BPMNEdge',{id:`${flow.getAttribute('id')}_sim_di`,bpmnElement:flow.getAttribute('id')??''},plane);
    add(dd,'di:waypoint',{x:a.x+a.w,y:a.y+a.h/2},edge);add(dd,'di:waypoint',{x:b.x,y:b.y+b.h/2},edge);
  });
  return new XMLSerializer().serializeToString(doc);
}
