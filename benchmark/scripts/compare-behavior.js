#!/usr/bin/env node
'use strict';

const fs = require('fs');

if (process.argv.length !== 4) {
  console.error('usage: compare-behavior.js CAMUNDA.json BPMNGO.json');
  process.exit(2);
}

const reference = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const candidate = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));

function normalize(value, field = '') {
  if (value === null) return null;
  if (Array.isArray(value)) {
    const normalized = value.map(item => normalize(item, field));
    if (normalized.every(item => item && typeof item === 'object' && !Array.isArray(item))) {
      normalized.sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
    }
    return normalized;
  }
  if (value && typeof value === 'object') {
    const out = {};
    for (const key of Object.keys(value).sort()) out[key] = normalize(value[key], key);
    return out;
  }
  // Engine-generated values differ legitimately between runs. Backoff is
  // compared by the scenario assertion (no early delivery, then delivery),
  // while its wall-clock observation is scheduling-dependent.
  if (/(Key|key)$/.test(field) || /(Date|Time|Cursor)$/.test(field) ||
      field === 'processDefinitionVersion' || field === 'redelivery_ms') {
    return '<generated>';
  }
  return value;
}

if (reference.scenarios.length !== candidate.scenarios.length) {
  console.error(`scenario count differs: ${reference.scenarios.length} vs ${candidate.scenarios.length}`);
  process.exit(1);
}

let identical = 0;
for (let i = 0; i < reference.scenarios.length; i++) {
  const expected = reference.scenarios[i];
  const actual = candidate.scenarios[i];
  const sameName = expected.name === actual.name;
  const same = sameName && JSON.stringify(normalize(expected.observations)) === JSON.stringify(normalize(actual.observations));
  if (same) {
    identical++;
  } else {
    console.error(`DIFF ${i + 1}: ${expected.name} vs ${actual.name}`);
    console.error('Camunda:', JSON.stringify(normalize(expected.observations)));
    console.error('BpmnGo:', JSON.stringify(normalize(actual.observations)));
  }
}

console.log(`IDENTICAL_NORMALIZED ${identical}/${reference.scenarios.length}`);
process.exit(identical === reference.scenarios.length ? 0 : 1);
