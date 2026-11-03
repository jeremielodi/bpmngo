/**
 * compare.ts — compares the traces of two engines, section by section.
 */
import { Trace } from './runner';

export type Verdict = 'same' | 'expected' | 'different' | 'missing';

export interface SectionResult {
  section: string;
  verdict: Verdict;
  a: string;                        // canonical JSON of engine A (reference)
  b: string;                        // canonical JSON of engine B
  note?: string;
}

export interface ScenarioResult {
  scenario: string;
  group: string;
  verdict: Verdict;
  sections: SectionResult[];
  infoA?: Trace['info'];
  infoB?: Trace['info'];
}

export interface KnownDifference {
  scenario: string;                 // a scenario name, or "*" for all
  section: string;
  note: string;
}

export const SECTIONS: (keyof Trace['compared'])[] = ['deploy', 'start', 'outcome', 'finalVariables', 'jobs', 'actions', 'instances'];

export function compareTraces(group: string, a: Trace | undefined, b: Trace | undefined, unordered: boolean, known: KnownDifference[]): ScenarioResult {
  const scenario = (a ?? b)!.scenario;
  if (!a || !b) {
    return { scenario, group, verdict: 'missing', sections: [], infoA: a?.info, infoB: b?.info };
  }
  const sections: SectionResult[] = SECTIONS.map(section => {
    let va: unknown = a.compared[section];
    let vb: unknown = b.compared[section];
    if (section === 'jobs' && unordered) {
      va = sortJobs(va as any[]);
      vb = sortJobs(vb as any[]);
    }
    const ca = canonical(va);
    const cb = canonical(vb);
    if (ca === cb) return { section, verdict: 'same' as Verdict, a: ca, b: cb };
    const k = known.find(k => (k.scenario === '*' || k.scenario === scenario) && k.section === section);
    return { section, verdict: (k ? 'expected' : 'different') as Verdict, a: ca, b: cb, note: k?.note };
  });
  const verdict: Verdict = sections.some(s => s.verdict === 'different') ? 'different'
    : sections.some(s => s.verdict === 'expected') ? 'expected' : 'same';
  return { scenario, group, verdict, sections, infoA: a.info, infoB: b.info };
}

function sortJobs(jobs: any[]): any[] {
  return [...jobs].sort((x, y) => canonical(x).localeCompare(canonical(y)));
}

/** JSON with sorted keys and numbers in one form (1 and 1.0 are the same). */
export function canonical(v: unknown): string {
  return JSON.stringify(normalise(v));
}

function normalise(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(normalise);
  if (v && typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v as object).sort()) out[k] = normalise((v as any)[k]);
    return out;
  }
  if (typeof v === 'number') return Number.isInteger(v) ? v : Number(v.toPrecision(15));
  return v;
}

// ── Report ────────────────────────────────────────────────────────────────────

const MARK: Record<Verdict, string> = { same: 'SAME     ', expected: 'EXPECTED ', different: 'DIFFERENT', missing: 'MISSING  ' };

export function textReport(results: ScenarioResult[], nameA: string, nameB: string, header: string[]): string {
  const lines: string[] = [];
  const count = (v: Verdict) => results.filter(r => r.verdict === v).length;
  lines.push(...header, '');
  lines.push(`Scenarios: ${results.length}   same: ${count('same')}   expected differences: ${count('expected')}   different: ${count('different')}   missing: ${count('missing')}`);
  lines.push('');
  let group = '';
  for (const r of results) {
    if (r.group !== group) {
      group = r.group;
      lines.push(`── ${group} ${'─'.repeat(Math.max(0, 70 - group.length))}`);
    }
    const diffs = r.sections.filter(s => s.verdict !== 'same').map(s => s.section);
    lines.push(`  ${MARK[r.verdict]}  ${r.scenario}${diffs.length ? `   (${diffs.join(', ')})` : ''}`);
  }

  const detailed = results.filter(r => r.verdict !== 'same');
  if (detailed.length) {
    lines.push('', '', '═'.repeat(78), 'DETAILS', '═'.repeat(78));
  }
  for (const r of detailed) {
    lines.push('', `▶ ${r.scenario}  [${r.verdict}]`);
    if (r.verdict === 'missing') {
      lines.push(`    a trace is missing: ${nameA} ${r.infoA ? 'present' : 'absent'}, ${nameB} ${r.infoB ? 'present' : 'absent'}`);
      continue;
    }
    for (const s of r.sections.filter(s => s.verdict !== 'same')) {
      lines.push(`  · ${s.section}${s.note ? `  — expected: ${s.note}` : ''}`);
      lines.push(...sideBySide(s, nameA, nameB).map(l => '      ' + l));
    }
    for (const [name, info] of [[nameA, r.infoA], [nameB, r.infoB]] as const) {
      const notes = [...(info?.errors ?? []), ...(info?.restNotes ?? [])];
      if (notes.length) lines.push(`  · ${name} notes:`, ...notes.map(n => `      ${n}`));
    }
  }
  return lines.join('\n') + '\n';
}

/** Shows only what differs: element by element for lists, key by key for objects. */
function sideBySide(s: SectionResult, nameA: string, nameB: string): string[] {
  const a = JSON.parse(s.a);
  const b = JSON.parse(s.b);
  const out: string[] = [];
  const w = Math.max(nameA.length, nameB.length);
  const line = (label: string, v: unknown) => `${label.padEnd(w)}: ${typeof v === 'string' ? v : JSON.stringify(v)}`;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) out.push(`count: ${nameA} ${a.length}, ${nameB} ${b.length}`);
    for (let i = 0; i < Math.max(a.length, b.length); i++) {
      const x = a[i];
      const y = b[i];
      if (canonical(x) === canonical(y)) continue;
      const isObj = (v: unknown) => v && typeof v === 'object' && !Array.isArray(v);
      // Two records of the same kind (e.g. two jobs): show only the fields that differ.
      if (isObj(x) && isObj(y)) {
        const label = x.type && x.elementId ? ` ${x.type} @ ${x.elementId}` : x.process ? ` ${x.process}` : '';
        out.push(`[${i}]${label}`);
        for (const k of [...new Set([...Object.keys(x), ...Object.keys(y)])].sort()) {
          if (canonical(x[k]) === canonical(y[k])) continue;
          out.push(`  ${k}:`, '    ' + line(nameA, k in x ? x[k] : '(absent)'), '    ' + line(nameB, k in y ? y[k] : '(absent)'));
        }
      } else {
        out.push(`[${i}]`, '  ' + line(nameA, x ?? '(none)'), '  ' + line(nameB, y ?? '(none)'));
      }
    }
  } else if (a && b && typeof a === 'object' && typeof b === 'object') {
    for (const k of [...new Set([...Object.keys(a), ...Object.keys(b)])].sort()) {
      if (canonical(a[k]) === canonical(b[k])) continue;
      out.push(`${k}:`, '  ' + line(nameA, k in a ? a[k] : '(absent)'), '  ' + line(nameB, k in b ? b[k] : '(absent)'));
    }
  } else {
    out.push(line(nameA, a), line(nameB, b));
  }
  return out;
}
