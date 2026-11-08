import { test, expect } from '@playwright/test';

test.use({trace:'off'});

const parallelBpmn = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test">
<process id="shared_team" name="Shared team" isExecutable="true">
<startEvent id="start"/><parallelGateway id="fork"/><task id="review" name="Review"/><task id="install" name="Installation"/><parallelGateway id="join"/><endEvent id="end"/>
<sequenceFlow id="s" sourceRef="start" targetRef="fork"/><sequenceFlow id="a" sourceRef="fork" targetRef="review"/><sequenceFlow id="b" sourceRef="fork" targetRef="install"/>
<sequenceFlow id="aj" sourceRef="review" targetRef="join"/><sequenceFlow id="bj" sourceRef="install" targetRef="join"/><sequenceFlow id="e" sourceRef="join" targetRef="end"/>
</process></definitions>`;

test('one shared pool supplies solo and team activities, with waiting and per-person costs', async ({page, request}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(15000);
  const login=await request.post('/auth/login',{data:{email:process.env.SUPER_USER_EMAIL??'admin@goflow.com',password:process.env.SUPER_USER_PASSWORD??'admin123'}});
  expect(login.status()).toBe(200);
  const tokens=await login.json(),headers={Authorization:`Bearer ${tokens.access_token}`};
  await page.addInitScript(tokens=>{localStorage.setItem('accessToken',tokens.access_token);localStorage.setItem('refreshToken',tokens.refresh_token);},tokens);
  let projectId='';
  const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message));
  const resourceRow=()=>page.getByRole('row').filter({has:page.getByRole('cell',{name:'team',exact:true})});
  const run=async()=>{
    const queued=page.waitForResponse(response=>response.url().endsWith('/runs')&&response.request().method()==='POST');
    await page.getByRole('button',{name:'Run experiment',exact:true}).click();
    const response=await queued;expect(response.status()).toBe(202);
    const id=(await response.json()).id;
    await expect.poll(async()=> (await (await request.get(`/engine-rest/simulation/runs/${id}`,{headers})).json()).status,{timeout:15000}).toBe('completed');
    return (await (await request.get(`/engine-rest/simulation/runs/${id}`,{headers})).json()).result;
  };
  try {
    await page.goto('/simulation');
    await page.getByLabel('Project name',{exact:true}).fill(`Shared team browser check ${Date.now()}`);
    const created=page.waitForResponse(response=>response.url().endsWith('/simulation/projects')&&response.request().method()==='POST');
    await page.getByRole('button',{name:'Save project',exact:true}).click();
    projectId=(await (await created).json()).id;
    await page.getByLabel('Upload project BPMN').setInputFiles({name:'team.bpmn',mimeType:'application/xml',buffer:Buffer.from(parallelBpmn)});
    await page.getByRole('button',{name:'Workload & resources',exact:true}).click();
    await page.getByRole('button',{name:'+ Add resource',exact:true}).click();
    const modal=page.getByRole('dialog',{name:'Add resource',exact:true});
    await modal.getByLabel('Resource ID').fill('team');
    await modal.getByRole('button',{name:'Add',exact:true}).click();
    await resourceRow().getByRole('spinbutton').nth(0).fill('4');
    await resourceRow().getByRole('spinbutton').nth(1).fill('60');
    await page.getByRole('combobox',{name:'Arrival model',exact:true}).selectOption('scheduled');
    await page.getByLabel('Replications',{exact:true}).fill('1');
    await page.getByRole('button',{name:'Project & models',exact:true}).click();
    for(const [id, people, minutes] of [['review','1','10'],['install','3','20']]){
      await page.locator(`[data-element-id="${id}"] .djs-hit`).click({position:{x:10,y:10}});
      await page.getByLabel('Resource pool',{exact:true}).selectOption('team');
      await page.getByLabel('People required',{exact:true}).fill(people);
      await page.getByLabel('Processing value',{exact:true}).fill(minutes);
    }
    await page.screenshot({path:'test-results/shared-team-constraints.png'});
    await page.getByRole('button',{name:'Configure workload →'}).click();
    const together=await run();
    expect(together.metrics.meanCycleMinutes).toBe(20);
    expect(together.metrics.meanCost).toBe(70);
    expect(together.metrics.resources[0].busyMinutes).toBe(70);
    const starts=together.metrics.trace.filter((event:any)=>event.kind==='start');
    expect(starts.map((event:any)=>event.resourceUnits).sort()).toEqual([1,3]);
    expect(starts.every((event:any)=>event.at===0)).toBe(true);
    await page.getByRole('button',{name:'Open',exact:true}).click();
    const teamStart=together.metrics.trace.findIndex((event:any)=>event.kind==='start'&&event.resourceUnits===3);
    await page.getByLabel('Replay position').fill(String(teamStart));
    await expect(page.getByText(/team · 3 people/)).toBeVisible();
    await page.getByRole('button',{name:'Workload & resources',exact:true}).click();
    await resourceRow().getByRole('spinbutton').nth(0).fill('3');
    const constrained=await run();
    expect(constrained.metrics.meanCycleMinutes).toBe(30);
    expect(constrained.metrics.meanCost).toBe(70);
    expect(constrained.metrics.trace.find((event:any)=>event.kind==='start'&&event.resourceUnits===3).at).toBe(10);
    expect(Object.values(constrained.scenarioSnapshot.activities).map((activity:any)=>activity.resourceUnits||1).sort()).toEqual([1,3]);
    expect(errors).toEqual([]);
  } finally {
    if(projectId) expect((await request.delete(`/engine-rest/simulation/projects/${projectId}`,{headers})).ok()).toBeTruthy();
  }
});
