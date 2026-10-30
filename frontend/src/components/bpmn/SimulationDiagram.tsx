import { useEffect, useRef, useState } from 'react';
// @ts-ignore bpmn-js does not publish TypeScript declarations
import BpmnJS from 'bpmn-js/lib/NavigatedViewer';
import { withDiagram } from '../../utils/simulationProject';

export type DiagramMark={id:string;kind:'visited'|'bottleneck'|'current';label?:string};

export function SimulationDiagram({xml,onSelect,marks=[]}:{xml:string;onSelect?:(id:string)=>void;marks?:DiagramMark[]}) {
  const container=useRef<HTMLDivElement>(null),viewer=useRef<any>(null);
  const select=useRef(onSelect);select.current=onSelect;
  const [ready,setReady]=useState(0),[error,setError]=useState('');
  useEffect(()=>{
	if(!container.current)return;
    const instance=new BpmnJS({container:container.current});viewer.current=instance;
    instance.on('element.click',(e:any)=>select.current?.(e.element.businessObject?.id??e.element.id));
    return ()=>{viewer.current=null;instance.destroy()};
  },[]);
  useEffect(()=>{
    let cancelled=false;setError('');
    viewer.current?.importXML(withDiagram(xml)).then(()=>{
      if(cancelled)return;viewer.current.get('canvas').zoom('fit-viewport');setReady(v=>v+1);
    }).catch((e:Error)=>{if(!cancelled)setError(e.message)});
    return ()=>{cancelled=true};
  },[xml]);
  useEffect(()=>{
    const instance=viewer.current;if(!instance||!ready)return;
    const registry=instance.get('elementRegistry'),canvas=instance.get('canvas'),overlays=instance.get('overlays');
    registry.getAll().forEach((element:any)=>['visited','bottleneck','current'].forEach(kind=>canvas.removeMarker(element.id,`sim-${kind}`)));
    overlays.remove({type:'simulation'});
    marks.forEach(mark=>{
      if(!registry.get(mark.id))return;
      canvas.addMarker(mark.id,`sim-${mark.kind}`);
      if(mark.label){const badge=document.createElement('span');badge.textContent=mark.label;badge.className=`sim-badge sim-badge-${mark.kind}`;overlays.add(mark.id,{type:'simulation',position:{top:-18,left:0},html:badge})}
    });
  },[marks,ready]);
  return <div className="relative border rounded-xl bg-white overflow-hidden">
    <style>{`.sim-visited .djs-visual > :is(path,rect,circle,polygon){stroke:#0d9488!important;stroke-width:3px!important}.sim-bottleneck .djs-visual > :is(path,rect,circle,polygon){stroke:#dc2626!important;stroke-width:4px!important}.sim-current .djs-visual > :is(path,rect,circle,polygon){stroke:#7c3aed!important;stroke-width:5px!important}.sim-badge{font-size:11px;background:#0f766e;color:white;padding:3px 6px;border-radius:5px;white-space:nowrap}.sim-badge-bottleneck{background:#dc2626}.sim-badge-current{background:#7c3aed}`}</style>
    {error&&<p role="alert" className="p-3 bg-amber-50 text-amber-900 text-sm">Diagram: {error}. Use the element selector to configure this model.</p>}
    <div ref={container} className="h-[520px]"/>
    <button type="button" className="absolute bottom-3 left-3 border rounded bg-white px-3 py-1 text-xs" onClick={()=>viewer.current?.get('canvas').zoom('fit-viewport')}>Fit diagram</button>
  </div>;
}
