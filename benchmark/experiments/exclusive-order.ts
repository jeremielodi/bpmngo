/**
 * Which flow an exclusive gateway takes when several conditions are true.
 * Deploys variants of one process (gateway -> tasks, every condition true)
 * and reports the element whose job is created. Run against Camunda to learn
 * its rule, and against BpmnGo to check it follows it:
 *
 *   npx ts-node --transpile-only experiments/exclusive-order.ts camunda localhost:26500 http://localhost:8080
 *   npx ts-node --transpile-only experiments/exclusive-order.ts bpmngo localhost:26600 http://localhost:9080 admin@bpmngo.com admin123
 */
import { Engine } from '../src/engine';

interface Flow { id: string; target: string; condition?: string }
interface Variant { name: string; flows: Flow[]; outgoing?: string[]; flowsFirst?: boolean; defaultFlow?: string }

const T = (s: string) => `=${s}`;
const variants: Variant[] = [
  { name: 'no-outgoing, doc order A,B', flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }] },
  { name: 'no-outgoing, doc order B,A', flows: [{ id: 'f_b', target: 'B', condition: T('true') }, { id: 'f_a', target: 'A', condition: T('true') }] },
  { name: 'no-outgoing, ids z then a', flows: [{ id: 'f_z', target: 'A', condition: T('true') }, { id: 'f_a', target: 'B', condition: T('true') }] },
  { name: 'no-outgoing, A,B,C', flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }, { id: 'f_c', target: 'C', condition: T('true') }] },
  { name: 'no-outgoing, A,B false,C', flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('false') }, { id: 'f_c', target: 'C', condition: T('true') }] },
  { name: 'no-outgoing, A,B,C false', flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }, { id: 'f_c', target: 'C', condition: T('false') }] },
  { name: 'no-outgoing, default first + A,B', defaultFlow: 'f_d', flows: [{ id: 'f_d', target: 'D' }, { id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }] },
  { name: 'no-outgoing, flows before gateway, A,B', flowsFirst: true, flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }] },
  { name: 'outgoing A,B, doc order A,B', outgoing: ['f_a', 'f_b'], flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }] },
  { name: 'outgoing B,A, doc order A,B', outgoing: ['f_b', 'f_a'], flows: [{ id: 'f_a', target: 'A', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }] },
  { name: 'outgoing A,B,C, doc order C,B,A', outgoing: ['f_a', 'f_b', 'f_c'], flows: [{ id: 'f_c', target: 'C', condition: T('true') }, { id: 'f_b', target: 'B', condition: T('true') }, { id: 'f_a', target: 'A', condition: T('true') }] },
];

function model(pid: string, v: Variant, run: string): string {
  const targets = [...new Set(v.flows.map(f => f.target))];
  const flows = v.flows.map(f => `<bpmn:sequenceFlow id="${f.id}" sourceRef="gw" targetRef="${f.target}">${f.condition ? `<bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">${f.condition}</bpmn:conditionExpression>` : ''}</bpmn:sequenceFlow>`).join('\n');
  const outgoing = (v.outgoing ?? []).map(id => `<bpmn:outgoing>${id}</bpmn:outgoing>`).join('');
  const gw = `<bpmn:exclusiveGateway id="gw"${v.defaultFlow ? ` default="${v.defaultFlow}"` : ''}><bpmn:incoming>f0</bpmn:incoming>${outgoing}</bpmn:exclusiveGateway>`;
  const tasks = targets.map(t => `<bpmn:serviceTask id="${t}"><bpmn:extensionElements><zeebe:taskDefinition type="xo-${run}-${t}"/></bpmn:extensionElements></bpmn:serviceTask>`).join('\n');
  const body = v.flowsFirst ? `${flows}\n${gw}` : `${gw}\n${flows}`;
  return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="d_${pid}" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="${pid}" isExecutable="true">
    <bpmn:startEvent id="s"><bpmn:outgoing>f0</bpmn:outgoing></bpmn:startEvent>
    <bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    ${body}
    ${tasks}
  </bpmn:process>
</bpmn:definitions>`;
}

async function main() {
  const [name, grpc, rest, user, password] = process.argv.slice(2);
  const engine = new Engine({ name, grpc, rest, user, password });
  const run = Date.now().toString(36);
  for (const [i, v] of variants.entries()) {
    const pid = `xo_${run}_${i}`;
    try {
      await engine.deploy(`${pid}.bpmn`, Buffer.from(model(pid, v, run)));
      await engine.call('CreateProcessInstance', { bpmnProcessId: pid, version: -1, variables: '{}' });
      const targets = [...new Set(v.flows.map(f => f.target))];
      let taken = '';
      for (let attempt = 0; attempt < 20 && !taken; attempt++) {
        for (const t of targets) {
          const jobs = await engine.activateJobs(`xo-${run}-${t}`, 1, 200);
          if (jobs.length) taken = t;
        }
      }
      console.log(`${v.name.padEnd(40)} -> ${taken || '(none)'}`);
    } catch (e: any) {
      console.log(`${v.name.padEnd(40)} -> error ${e.code ?? ''} ${e.message ?? e}`);
    }
  }
  engine.close();
}

main();
