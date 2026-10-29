import type { Scenario } from '../api/simulation';

const supportOperationsBpmn = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="https://bpmngo.dev/simulation">
  <process id="support_operations" name="Support operations" isExecutable="true">
    <startEvent id="StartEvent_1wsxujp"/><userTask id="Activity_0rt6ids" name="Check request"/>
    <exclusiveGateway id="Gateway_1i01pjg"/><userTask id="Activity_0ouwyd5" name="Send known resolution"/>
    <userTask id="Activity_1npzcct" name="Evaluate request"/><exclusiveGateway id="Gateway_18x5arg"/>
    <userTask id="Activity_1kwn3zv" name="Solve issue"/><userTask id="Activity_1afil2e" name="Write resolution"/>
    <userTask id="Activity_1pt3bpc" name="Send resolution"/><receiveTask id="Activity_0pwue36" name="Wait for client response"/>
    <exclusiveGateway id="Gateway_12y6m03"/><userTask id="Activity_1lho2iu" name="Forward to level 2"/><endEvent id="EndEvent_done"/>
    <sequenceFlow id="f_start" sourceRef="StartEvent_1wsxujp" targetRef="Activity_0rt6ids"/>
    <sequenceFlow id="f_check" sourceRef="Activity_0rt6ids" targetRef="Gateway_1i01pjg"/>
    <sequenceFlow id="Flow_1pizyqk" sourceRef="Gateway_1i01pjg" targetRef="Activity_0ouwyd5"/>
    <sequenceFlow id="Flow_10wb9z2" sourceRef="Gateway_1i01pjg" targetRef="Activity_1npzcct"/>
    <sequenceFlow id="f_known_end" sourceRef="Activity_0ouwyd5" targetRef="EndEvent_done"/>
    <sequenceFlow id="f_evaluate" sourceRef="Activity_1npzcct" targetRef="Gateway_18x5arg"/>
    <sequenceFlow id="f_merge_solve" sourceRef="Gateway_18x5arg" targetRef="Activity_1kwn3zv"/>
    <sequenceFlow id="f_solve_write" sourceRef="Activity_1kwn3zv" targetRef="Activity_1afil2e"/>
    <sequenceFlow id="f_write_send" sourceRef="Activity_1afil2e" targetRef="Activity_1pt3bpc"/>
    <sequenceFlow id="f_send_wait" sourceRef="Activity_1pt3bpc" targetRef="Activity_0pwue36"/>
    <sequenceFlow id="f_wait_decide" sourceRef="Activity_0pwue36" targetRef="Gateway_12y6m03"/>
    <sequenceFlow id="Flow_1eqnu7k" sourceRef="Gateway_12y6m03" targetRef="EndEvent_done"/>
    <sequenceFlow id="Flow_0g0xphy" sourceRef="Gateway_12y6m03" targetRef="Activity_1lho2iu"/>
    <sequenceFlow id="Flow_0wxfi6y" sourceRef="Activity_1lho2iu" targetRef="Gateway_18x5arg"/>
  </process>
</definitions>`;

export const emptyScenario = (): Scenario => ({
  name: 'Untitled simulation',
  bpmnXml: '',
  arrivalRatePerHour: 1,
  durationHours: 168,
  replications: 10,
  seed: 1,
  resources: [],
  activities: {},
  flows: {},
});

export const supportOperationsExample: Scenario = {
  name: 'Support operations — baseline', bpmnXml: supportOperationsBpmn, arrivalRatePerHour: 1, durationHours: 720, replications: 10, seed: 20261029,
  resources: [{ id:'level1',name:'Level 1 agents',capacity:5,costPerHour:40 },{ id:'level2',name:'Level 2 agents',capacity:3,costPerHour:60 }],
  activities: {
    Activity_0rt6ids:{processingTime:{type:'fixed',value:10},waitingTime:{type:'fixed',value:60},resourceId:'level1',valueAdded:true},
    Activity_0ouwyd5:{processingTime:{type:'normal',mean:5,standardDeviation:3},resourceId:'level1',valueAdded:true},
    Activity_1npzcct:{processingTime:{type:'fixed',value:20},waitingTime:{type:'fixed',value:120},resourceId:'level2',valueAdded:true},
    Activity_1kwn3zv:{processingTime:{type:'fixed',value:120},waitingTime:{type:'fixed',value:1200},resourceId:'level2',valueAdded:true},
    Activity_1afil2e:{processingTime:{type:'fixed',value:20},resourceId:'level2',valueAdded:true},
    Activity_1pt3bpc:{processingTime:{type:'normal',mean:20,standardDeviation:5},waitingTime:{type:'fixed',value:1200},resourceId:'level1',valueAdded:true},
    Activity_0pwue36:{processingTime:{type:'fixed',value:0},waitingTime:{type:'fixed',value:1200},valueAdded:false},
    Activity_1lho2iu:{processingTime:{type:'fixed',value:2},resourceId:'level1',valueAdded:false},
  },
  flows:{Flow_1pizyqk:{probability:.2},Flow_10wb9z2:{probability:.8},Flow_1eqnu7k:{probability:.8},Flow_0g0xphy:{probability:.2}},
};
