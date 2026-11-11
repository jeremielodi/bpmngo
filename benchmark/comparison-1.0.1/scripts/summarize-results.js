#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const root = path.resolve(__dirname, '..', '..');

function read(p) { return fs.readFileSync(path.join(root, p), 'utf8'); }
function json(p) { return JSON.parse(read(p)); }
function parseCgroup(text) {
  const out = {};
  for (const line of text.trim().split(/\n/)) {
    const m = line.match(/^([^ ]+)\s+(\d+)$/);
    if (m) out[m[1]] = Number(m[2]);
  }
  return out;
}
function bytes(s) {
  const m = String(s).trim().match(/^([\d.]+)([KMGT]?i?B)$/i);
  if (!m) return Number(s) || 0;
  const powers = { B:0, KB:1, KIB:1, MB:2, MIB:2, GB:3, GIB:3, TB:4, TIB:4 };
  const base = /iB$/i.test(m[2]) ? 1024 : 1000;
  return Number(m[1]) * base ** powers[m[2].toUpperCase()];
}
function stats(p) {
  return read(p).trim().split(/\n/).filter(l => l.startsWith('{')).map(l => JSON.parse(l));
}
function groupSamples(rows, cutoff) {
  const groups = new Map();
  for (const row of rows) {
    if (new Date(row.sampledAt) < cutoff) continue;
    const g = groups.get(row.sampledAt) || { cpu:0, memory:0, byContainer:{} };
    const cpu = Number(String(row.CPUPerc).replace('%',''));
    g.cpu += cpu;
    g.memory += bytes(String(row.MemUsage).split('/')[0]);
    g.byContainer[row.Name] = cpu;
    groups.set(row.sampledAt, g);
  }
  return [...groups.values()];
}
function measuredDir(rel, warmupSeconds=0) {
  const dir = path.join(root, rel);
  const before = {}, after = {};
  for (const f of fs.readdirSync(dir)) {
    if (f.endsWith('-cgroup-before.txt')) before[f.slice(0,-18)] = parseCgroup(fs.readFileSync(path.join(dir,f),'utf8'));
    if (f.endsWith('-cgroup-after.txt')) after[f.slice(0,-17)] = parseCgroup(fs.readFileSync(path.join(dir,f),'utf8'));
  }
  const started = new Date(fs.readFileSync(path.join(dir,'started-at.txt'),'utf8').trim());
  const finished = new Date(fs.readFileSync(path.join(dir,'finished-at.txt'),'utf8').trim());
  const duration = (finished-started)/1000;
  const cpuByContainer = {};
  let cpuSeconds = 0, idleMemory = 0;
  for (const c of Object.keys(before)) {
    const sec = ((after[c]?.usage_usec || 0) - (before[c]?.usage_usec || 0))/1e6;
    cpuByContainer[c] = sec; cpuSeconds += sec; idleMemory += before[c]['memory.current'] || 0;
  }
  const samples = groupSamples(stats(path.join(rel,'docker-stats.ndjson')), new Date(started.getTime()+warmupSeconds*1000));
  const avg = key => samples.length ? samples.reduce((a,x)=>a+x[key],0)/samples.length : 0;
  const peak = key => samples.length ? Math.max(...samples.map(x=>x[key])) : 0;
  const avgCPUByContainer = {};
  const names = new Set(samples.flatMap(s=>Object.keys(s.byContainer)));
  for (const name of names) avgCPUByContainer[name] = samples.reduce((a,s)=>a+(s.byContainer[name]||0),0)/samples.length;
  return {duration_seconds:duration,cpu_seconds:cpuSeconds,cpu_seconds_by_container:cpuByContainer,cpu_average_percent_exact:cpuSeconds/duration*100,cpu_average_percent_sampled:avg('cpu'),cpu_peak_percent_sampled:peak('cpu'),memory_idle_bytes_cgroup:idleMemory,memory_idle_bytes_sampled:samples[0]?.memory || 0,memory_peak_bytes_sampled:peak('memory'),sample_count:samples.length,cpu_average_percent_by_container_sampled:avgCPUByContainer};
}

const out = {behavior:{},latency:{},load:[]};
out.behavior.camunda = measuredDir('benchmark/raw/camunda/behavior');
out.behavior.bpmngo = measuredDir('benchmark/raw/bpmngo/behavior');
for (const engine of ['camunda','bpmngo']) {
  out.latency[engine] = [1,2].map(p => json(`benchmark/raw/${engine}/latency-pass${p}.json`).summary);
}
for (const [folder,label] of [['camunda-8.10.7','camunda'],['bpmngo-1.0.1','bpmngo']]) {
  for (const pass of [1,2]) for (const clients of [10,50,100]) {
    const rel=`benchmark/raw/${folder}/load-${clients}-pass${pass}`;
    const result=json(`${rel}/result.json`);
    const resources=measuredDir(rel,10);
    resources.cpu_seconds_per_instance_estimated_from_sampled_average=(resources.cpu_average_percent_sampled/100*result.seconds)/result.completed;
    out.load.push({engine:label,pass,clients,result,resources});
  }
}
fs.writeFileSync(path.join(root,'benchmark/raw/summary.json'),JSON.stringify(out,null,2)+'\n');
console.log(JSON.stringify(out,null,2));
