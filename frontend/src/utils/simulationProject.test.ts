import { describe,it,expect } from 'vitest';
import { emptyScenario } from '../data/simulationExamples';
import { asProject,upsertProjectModel,withDiagram,modelElements } from './simulationProject';

const xml=`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="process" name="Orders"><startEvent id="start"/><task id="work"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="work"/><sequenceFlow id="b" sourceRef="work" targetRef="end"/></process></definitions>`;

describe('Simulation projects',()=>{
  it('isolates duplicate BPMN IDs across models and preserves constraints when replacing a model',()=>{
    let s=upsertProjectModel(emptyScenario(),xml,'Orders','one');
    s.activities['one::work'].fixedCost=17;
    s=upsertProjectModel(s,xml,'Fulfillment','two');
    expect(Object.keys(s.activities)).toEqual(['one::work','two::work']);
    s=upsertProjectModel(s,xml,'Orders','one');
    expect(s.activities['one::work'].fixedCost).toBe(17);
    expect(s.activities['two::work']).toBeDefined();
    expect(s.models).toHaveLength(2);
  });
  it('converts existing scenarios once without losing parameter values',()=>{
    const legacy={...emptyScenario(),bpmnXml:xml,activities:{work:{processingTime:{type:'fixed' as const,value:42},valueAdded:true}}};
    const project=asProject(legacy);
    expect(project.activities['main::work'].processingTime.value).toBe(42);
    expect(asProject(project)).toEqual(project);
  });
  it('preserves event constraints and project model names when saving from the modeler',()=>{
    const waitingXML=xml.replace('<task id="work"/>','<intermediateCatchEvent id="work"/>');
    let project=upsertProjectModel(emptyScenario(),waitingXML,'Orders','one');
    project.models![0].name='Regional orders';
    project.activities['one::work']={processingTime:{type:'fixed',value:0},waitingTime:{type:'fixed',value:60},fixedCost:5,valueAdded:false};
    const saved=upsertProjectModel(project,waitingXML,'process','one');
    expect(saved.models![0].name).toBe('Regional orders');
    expect(saved.activities['one::work']).toEqual(project.activities['one::work']);
  });
  it('adds a diagram for semantic-only imports and preserves existing diagram positions',()=>{
    const diagram=withDiagram(xml);
    const document=new DOMParser().parseFromString(diagram,'application/xml');
    expect(document.querySelector('parsererror')).toBeNull();
    expect(document.getElementsByTagNameNS('*','BPMNShape')).toHaveLength(3);
    expect(document.getElementsByTagNameNS('*','BPMNEdge')).toHaveLength(2);
    expect(withDiagram(diagram)).toBe(diagram);
    expect(modelElements(diagram).filter(e=>e.topLevel&&e.type!=='sequenceFlow')).toHaveLength(3);
  });
});
