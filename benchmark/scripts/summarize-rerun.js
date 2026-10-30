#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const root = path.resolve(__dirname, '..', '..');
const run = process.argv[2] || 'rerun-20261029';
const base = `benchmark/raw/${run}`;

function read(rel) { return fs.readFileSync(path.join(root, rel), 'utf8'); }
function json(rel) { return JSON.parse(read(rel)); }
function parseCgroup(text) {
  const out = {};
  for (const line of text.trim().split(/\n/)) {
    const match = line.match(/^([^ ]+)\s+(\d+)$/);
    if (match) out[match[1]] = Number(match[2]);
  }
  return out;
}
function bytes(value) {
  const match = String(value).trim().match(/^([\d.]+)([KMGT]?i?B)$/i);
  if (!match) return Number(value) || 0;
  const powers = {B: 0, KB: 1, KIB: 1, MB: 2, MIB: 2, GB: 3, GIB: 3, TB: 4, TIB: 4};
  const radix = /iB$/i.test(match[2]) ? 1024 : 1000;
  return Number(match[1]) * radix ** powers[match[2].toUpperCase()];
}
function stats(rel) {
  return read(rel).trim().split(/\n/).filter(line => line.startsWith('{')).map(line => JSON.parse(line));
}
function samples(rows, cutoff) {
  const groups = new Map();
  for (const row of rows) {
    if (new Date(row.sampledAt) < cutoff) continue;
    const group = groups.get(row.sampledAt) || {cpu: 0, memory: 0, byContainer: {}};
    const cpu = Number(String(row.CPUPerc).replace('%', ''));
    group.cpu += cpu;
    group.memory += bytes(String(row.MemUsage).split('/')[0]);
    group.byContainer[row.Name] = cpu;
    groups.set(row.sampledAt, group);
  }
  return [...groups.values()];
}
function measuredDir(rel, warmupSeconds = 0) {
  const dir = path.join(root, rel);
  const before = {}, after = {};
  for (const file of fs.readdirSync(dir)) {
    if (file.endsWith('-cgroup-before.txt')) before[file.slice(0, -18)] = parseCgroup(fs.readFileSync(path.join(dir, file), 'utf8'));
    if (file.endsWith('-cgroup-after.txt')) after[file.slice(0, -17)] = parseCgroup(fs.readFileSync(path.join(dir, file), 'utf8'));
  }
  const started = new Date(fs.readFileSync(path.join(dir, 'started-at.txt'), 'utf8').trim());
  const finished = new Date(fs.readFileSync(path.join(dir, 'finished-at.txt'), 'utf8').trim());
  const duration = (finished - started) / 1000;
  const cpuByContainer = {};
  let cpuSeconds = 0;
  let idleCgroup = 0;
  for (const container of Object.keys(before)) {
    const seconds = ((after[container]?.usage_usec || 0) - (before[container]?.usage_usec || 0)) / 1e6;
    cpuByContainer[container] = seconds;
    cpuSeconds += seconds;
    idleCgroup += before[container]['memory.current'] || 0;
  }
  const grouped = samples(stats(`${rel}/docker-stats.ndjson`), new Date(started.getTime() + warmupSeconds * 1000));
  const average = field => grouped.length ? grouped.reduce((sum, item) => sum + item[field], 0) / grouped.length : 0;
  const peak = field => grouped.length ? Math.max(...grouped.map(item => item[field])) : 0;
  const cpuByContainerSampled = {};
  const names = new Set(grouped.flatMap(item => Object.keys(item.byContainer)));
  for (const name of names) cpuByContainerSampled[name] = grouped.reduce((sum, item) => sum + (item.byContainer[name] || 0), 0) / grouped.length;
  return {
    duration_seconds: duration,
    cpu_seconds: cpuSeconds,
    cpu_seconds_by_container: cpuByContainer,
    cpu_average_percent_exact: cpuSeconds / duration * 100,
    cpu_average_percent_sampled: average('cpu'),
    cpu_peak_percent_sampled: peak('cpu'),
    memory_idle_bytes_cgroup: idleCgroup,
    memory_idle_bytes_sampled: grouped[0]?.memory || 0,
    memory_peak_bytes_sampled: peak('memory'),
    sample_count: grouped.length,
    cpu_average_percent_by_container_sampled: cpuByContainerSampled,
  };
}

const output = {run, behavior: {}, latency: {}, load: []};
for (const engine of ['camunda', 'bpmngo']) {
  const behavior = json(`${base}/${engine}/behavior/result.json`);
  output.behavior[engine] = {
    scenarios: behavior.scenarios.length,
    errors: behavior.scenarios.filter(item => item.error).map(item => ({name: item.name, error: item.error})),
    resources: measuredDir(`${base}/${engine}/behavior`),
  };
  output.latency[engine] = [1, 2].map(pass => json(`${base}/${engine}/latency-pass${pass}.json`).summary);
  for (const pass of [1, 2]) for (const clients of [10, 50, 100]) {
    const rel = `${base}/${engine}/load-${clients}-pass${pass}`;
    const result = json(`${rel}/result.json`);
    const resources = measuredDir(rel, 10);
    resources.cpu_seconds_per_instance_estimated = (resources.cpu_average_percent_sampled / 100 * result.seconds) / result.completed;
    output.load.push({engine, pass, clients, result, resources});
  }
}

fs.writeFileSync(path.join(root, base, 'summary.json'), JSON.stringify(output, null, 2) + '\n');
console.log(JSON.stringify(output, null, 2));
