import { test,expect } from '@playwright/test';

const bpmn=(name:string)=>`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test"><process id="process" name="${name}" isExecutable="true"><startEvent id="start"/><task id="work" name="Work"/><endEvent id="end"/><sequenceFlow id="a" sourceRef="start" targetRef="work"/><sequenceFlow id="b" sourceRef="work" targetRef="end"/></process></definitions>`;

test('project: upload models, edit constraints on diagram, hand off a case, replay, and save from modeler',async({page,request})=>{
  test.setTimeout(90000);
  page.setDefaultTimeout(15000);
  const login=await request.post('/auth/login',{data:{email:process.env.SUPER_USER_EMAIL??'admin@goflow.com',password:process.env.SUPER_USER_PASSWORD??'admin123'}});
  expect(login.ok()).toBeTruthy();
  const {access_token}=await login.json(),headers={Authorization:`Bearer ${access_token}`};
  await page.addInitScript(token=>localStorage.setItem('accessToken',token),access_token);
  let projectId='';
  const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
  try{
    await page.goto('/simulation');
    await page.getByLabel('Project name',{exact:true}).fill(`Project UX test ${Date.now()}`);
    const created=page.waitForResponse(r=>r.url().endsWith('/simulation/projects')&&r.request().method()==='POST');
    await page.getByRole('button',{name:'Save project',exact:true}).click();
    const response=await created;expect(response.status()).toBe(201);projectId=(await response.json()).id;
    await page.getByLabel('Upload project BPMN').setInputFiles([
      {name:'orders.bpmn',mimeType:'application/xml',buffer:Buffer.from(bpmn('Orders A'))},
      {name:'fulfillment.bpmn',mimeType:'application/xml',buffer:Buffer.from(bpmn('Orders B'))},
    ]);
    await page.getByRole('button',{name:/^Orders A/}).click();
    await page.locator('[data-element-id="work"] .djs-hit').click();
    await page.getByLabel('Processing value',{exact:true}).fill('2');
    await page.getByLabel('Fixed cost per visit').fill('5');
    await page.locator('[data-element-id="end"] .djs-hit').click();
    await page.getByLabel('Handoff target model').selectOption({label:'Orders B'});
    await page.getByLabel('Transit delay value').fill('5');
    await page.getByLabel('Handoff cost').fill('3');
    await page.getByRole('button',{name:'Orders B',exact:true}).click();
    await page.locator('[data-element-id="work"] .djs-hit').click();
    await page.getByLabel('Processing value',{exact:true}).fill('4');
    await page.getByLabel('Waiting value',{exact:true}).fill('3');
    await page.getByLabel('Fixed cost per visit').fill('7');
    await page.getByRole('button',{name:'Configure workload →'}).click();
    await page.getByRole('combobox',{name:'Arrival model',exact:true}).selectOption('scheduled');
    await page.getByLabel('Replications',{exact:true}).fill('1');
    const queued=page.waitForResponse(r=>r.url().endsWith('/runs')&&r.request().method()==='POST');
    await page.getByRole('button',{name:'Run experiment',exact:true}).click();
    const queuedResponse=await queued;expect(queuedResponse.status(),await queuedResponse.text()).toBe(202);
    const runId=(await queuedResponse.json()).id;
    await expect.poll(async()=>{
      const r=await request.get(`/engine-rest/simulation/runs/${runId}`,{headers});return (await r.json()).status;
    }).toBe('completed');
    const run=await (await request.get(`/engine-rest/simulation/runs/${runId}`,{headers})).json();
    expect(run.result.metrics.completed).toBe(1);
    expect(run.result.metrics.meanCycleMinutes).toBe(14);
    expect(run.result.metrics.meanCost).toBe(15);
    expect(run.result.scenarioSnapshot.models).toHaveLength(2);
    expect(new Set(run.result.metrics.trace.map((e:any)=>e.caseId)).size).toBe(1);
    await page.getByRole('button',{name:'Open',exact:true}).click();
    await expect(page.getByRole('heading',{name:'As-run paths and bottlenecks'})).toBeVisible();
    await page.getByLabel('Replay model').selectOption({label:'Orders B'});
    await expect(page.locator('[data-element-id="work"].sim-bottleneck')).toHaveCount(1);
    await page.getByRole('button',{name:'Handoff: Orders A → Orders B',exact:true}).click();
    await expect(page.locator('[data-element-id="end"].sim-bottleneck')).toHaveCount(1);
    await page.getByLabel('Replay position').fill('0');
    await expect(page.locator('.sim-current')).not.toHaveCount(0);
    await page.screenshot({path:'test-results/simulation-project-replay.png',fullPage:true});
    const download=page.waitForEvent('download');await page.getByRole('button',{name:'Export execution log'}).click();expect((await download).suggestedFilename()).toBe('simulation-execution-log.json');
    await page.getByRole('button',{name:'Issues',exact:true}).click();
    await expect(page.getByRole('heading',{name:'Issue register',exact:true})).toBeVisible();
    await page.getByRole('button',{name:'Calibration',exact:true}).click();
    await expect(page.getByRole('heading',{name:'Historical calibration',exact:true})).toBeVisible();
    await page.getByRole('button',{name:'Project & models',exact:true}).click();
    await page.getByRole('button',{name:'Edit BPMN',exact:true}).click();
    await expect(page.getByRole('heading',{name:'Project BPMN Modeler'})).toBeVisible();
    await expect(page.getByRole('button',{name:'Save to project'})).toBeEnabled();
    await page.getByRole('button',{name:'Save to project'}).click();
    await expect(page).toHaveURL(new RegExp(`/simulation\\?project=${projectId}`));
    await expect(page.getByRole('button',{name:'Orders A Workload entry',exact:true})).toBeVisible();
    const stored=await (await request.get(`/engine-rest/simulation/projects/${projectId}`,{headers})).json();
    expect(stored.configuration.models).toHaveLength(2);
    expect(stored.configuration.transfers).toHaveLength(1);
    expect(stored.currentVersion).toBeGreaterThan(2);
    // Editing a model does not change the historical run snapshot.
    const previous=await (await request.get(`/engine-rest/simulation/runs/${runId}`,{headers})).json();
    expect(previous.result.scenarioSnapshot).toEqual(run.result.scenarioSnapshot);
    // Create a third, unfinished model directly in the modeler and save it
    // without requiring a runnable process or deploying to the workflow engine.
    await page.getByRole('button',{name:'Create in modeler'}).click();
    await expect(page.locator('[data-element-id="StartEvent_1"] .djs-hit')).toBeVisible();
    await page.getByRole('button',{name:'Save to project'}).click();
    await expect(page).toHaveURL(new RegExp(`/simulation\\?project=${projectId}`));
    await expect(page.getByRole('button',{name:'New Process',exact:true})).toBeVisible();
    const expanded=await (await request.get(`/engine-rest/simulation/projects/${projectId}`,{headers})).json();
    expect(expanded.configuration.models).toHaveLength(3);
    expect(expanded.configuration.transfers).toEqual(stored.configuration.transfers);
    expect(expanded.configuration.activities).toEqual(stored.configuration.activities);
    expect(errors).toEqual([]);
  }finally{
    if(errors.length)console.error('Browser errors:',errors);
    if(projectId)expect((await request.delete(`/engine-rest/simulation/projects/${projectId}`,{headers})).ok()).toBeTruthy();
  }
});

test('project revisions, portable packages and campaigns retain all models and assumptions', async ({request}) => {
  const login = await request.post('/auth/login', {data: {
    email: process.env.SUPER_USER_EMAIL ?? 'admin@goflow.com',
    password: process.env.SUPER_USER_PASSWORD ?? 'admin123',
  }});
  expect(login.ok()).toBeTruthy();
  const {access_token} = await login.json();
  const headers = {Authorization: `Bearer ${access_token}`};
  const projectIds: string[] = [];
  const scenario = {
    name: `Project lifecycle test ${Date.now()}`, bpmnXml: '', entryModelId: 'a',
    models: [{id:'a',name:'Orders',bpmnXml:bpmn('Orders')}, {id:'b',name:'Delivery',bpmnXml:bpmn('Delivery')}],
    transfers: [{id:'shipping',sourceModelId:'a',sourceNodeId:'end',targetModelId:'b',targetNodeId:'start',delay:{type:'fixed',value:5},cost:3}],
    arrivalRatePerHour:1,durationHours:24,replications:1,seed:42,
    arrivalPattern:{type:'scheduled',scheduledMinutes:[0]},
    startTime:'2026-11-02T09:00:00Z',
    calendars:[{id:'office',name:'Office',timezone:'UTC',shifts:[{weekdays:[1,2,3,4,5],startMinute:540,endMinute:1020}]}],
    resources:[{id:'team',name:'Shared team',capacity:1,costPerHour:60,calendarId:'office',skills:['review']}],
    activities:{
      'a::work':{processingTime:{type:'fixed',value:2},fixedCost:5,requiredSkills:['review'],valueAdded:true},
      'b::work':{processingTime:{type:'fixed',value:4},fixedCost:7,requiredSkills:['review'],valueAdded:true},
    },flows:{},
  };
  const completed = async (id:string) => {
    await expect.poll(async()=>{
      const run=await (await request.get(`/engine-rest/simulation/runs/${id}`,{headers})).json();
      return run.status;
    },{timeout:15000}).toBe('completed');
    return (await request.get(`/engine-rest/simulation/runs/${id}`,{headers})).json();
  };
  try {
    const created=await request.post('/engine-rest/simulation/projects',{headers,data:scenario});
    expect(created.status()).toBe(201);
    const project=await created.json();projectIds.push(project.id);
    const path=`/engine-rest/simulation/scenarios/${project.id}`;
    const queued=await request.post(`${path}/runs`,{headers});
    expect(queued.status()).toBe(202);
    const initialRun=await queued.json();
    // The enqueue response itself proves the request captured the old version,
    // independent of whether a worker starts before or after this edit.
    expect(initialRun.request.scenarioVersion).toBe(1);
    expect(initialRun.request.scenarioSnapshot.activities['b::work'].processingTime.value).toBe(4);
    const changed=structuredClone(scenario);changed.activities['b::work'].processingTime.value=20;
    expect((await request.put(`/engine-rest/simulation/projects/${project.id}`,{headers,data:changed})).ok()).toBeTruthy();
    const first=await completed(initialRun.id);
    expect(first.result.metrics.meanCycleMinutes).toBe(11);
    expect(first.result.metrics.meanCost).toBe(21);
    expect(first.result.scenarioSnapshot.activities['b::work'].processingTime.value).toBe(4);
    const restored=await request.post(`${path}/versions/1/restore`,{headers});
    expect(restored.ok()).toBeTruthy();
    const restoredProject=await restored.json();
    expect(restoredProject.currentVersion).toBe(3);
    expect(restoredProject.configuration.models).toEqual(scenario.models);
    expect(restoredProject.configuration.activities).toEqual(project.configuration.activities);
    const versions=await (await request.get(`${path}/versions`,{headers})).json();
    expect(versions).toHaveLength(3);
    const exported=await (await request.get(`${path}/export`,{headers})).json();
    expect(exported.scenario.transfers).toEqual(scenario.transfers);
    const imported=await request.post('/engine-rest/simulation/scenarios/import',{headers,data:exported});
    expect(imported.status()).toBe(201);
    const importedProject=await imported.json();projectIds.push(importedProject.id);
    expect(importedProject.configuration).toEqual(restoredProject.configuration);
    const swept=await request.post(`${path}/capacity-sweeps`,{headers,data:{resourceId:'team',minimumCapacity:1,maximumCapacity:2,step:1}});
    expect(swept.status()).toBe(202);
    expect((await completed((await swept.json()).id)).result.points).toHaveLength(2);
    const experiment=await request.post(`${path}/experiments`,{headers,data:{dimensions:[
      {path:'activity.b::work.processingTime.value',values:[4,8]},
      {path:'resource.team.costPerHour',values:[60,120]},
    ],objective:'cost'}});
    expect(experiment.status()).toBe(202);
    const points=(await completed((await experiment.json()).id)).result.points;
    expect(points).toHaveLength(4);
    expect(points.every((point:any)=>!point.error)).toBe(true);
    expect(points.map((point:any)=>point.meanCycleMinutes).sort()).toEqual([11,11,15,15]);
    const issue=await request.post(`${path}/issues`,{headers,data:{title:'Review transfer delay',code:'MANUAL',evidence:'Project lifecycle test',category:'other',severity:'medium',suggestion:'Compare alternatives',status:'open'}});
    expect(issue.status()).toBe(201);
    expect((await (await request.get(`${path}/issues`,{headers})).json()).some((item:any)=>item.title==='Review transfer delay')).toBe(true);
  } finally {
    for (const id of projectIds) expect((await request.delete(`/engine-rest/simulation/projects/${id}`,{headers})).ok()).toBeTruthy();
  }
});
