/**
 * resources.ts — CPU and memory used by each engine's Docker containers
 * while its scenarios run.
 *
 * CPU time is exact: the containers' cgroup counter (cpu.stat, usage_usec)
 * is read before and after the run. Peaks and memory come from
 * `docker stats` samples taken back to back during the run (one every
 * ~2 s, the time docker stats needs to measure). Memory "idle" is the
 * sample taken just before the run.
 */
import { execFile } from 'child_process';

export interface ContainerUsage {
  container: string;
  cpuSeconds: number | null;   // CPU time used during the run (null: counter unreadable)
  cpuAvgPct: number;           // average CPU, in % of one core
  cpuPeakPct: number;          // highest sample, in % of one core
  memIdleMiB: number;          // just before the run
  memAvgMiB: number;
  memPeakMiB: number;
  memLimitMiB: number;         // the container's limit (or the host's memory)
  cpuLimit: string;            // "cores 2,3", "1.5 CPUs" or "none"
}

export interface EngineUsage {
  engine: string;
  durationS: number;
  samples: number;
  containers: ContainerUsage[];
  total: Omit<ContainerUsage, 'container' | 'memLimitMiB' | 'cpuLimit'>;
}

interface Sample { cpuPct: number; memMiB: number; limitMiB: number }

function docker(args: string[], timeoutMs = 15000): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile('docker', args, { timeout: timeoutMs, windowsHide: true }, (err, stdout) => {
      if (err) reject(err);
      else resolve(stdout);
    });
  });
}

/** "512.3MiB", "1.2GiB", "800kB" → MiB. */
function toMiB(s: string): number {
  const m = /^([\d.]+)\s*([KMGT]?i?B)$/i.exec(s.trim());
  if (!m) return NaN;
  const v = parseFloat(m[1]);
  const unit = m[2].toUpperCase();
  const factor: Record<string, number> = {
    B: 1 / 1048576, KB: 1000 / 1048576, KIB: 1 / 1024, MB: 1e6 / 1048576, MIB: 1,
    GB: 1e9 / 1048576, GIB: 1024, TB: 1e12 / 1048576, TIB: 1048576,
  };
  return v * (factor[unit] ?? NaN);
}

/** One `docker stats` sample of each container. */
async function sample(containers: string[]): Promise<Record<string, Sample>> {
  const out: Record<string, Sample> = {};
  const text = await docker(['stats', '--no-stream', '--format', '{{json .}}', ...containers]);
  for (const line of text.split('\n').filter(l => l.trim())) {
    const s = JSON.parse(line);
    const [used, limit] = String(s.MemUsage).split('/');
    out[s.Name] = { cpuPct: parseFloat(s.CPUPerc), memMiB: toMiB(used), limitMiB: toMiB(limit) };
  }
  return out;
}

/** CPU time the container has used since it started, in µs (cgroup v2, then v1). */
async function cpuUsec(container: string): Promise<number | null> {
  try {
    const v2 = await docker(['exec', container, 'cat', '/sys/fs/cgroup/cpu.stat']);
    const m = /usage_usec (\d+)/.exec(v2);
    if (m) return Number(m[1]);
  } catch { /* try v1 */ }
  try {
    const v1 = await docker(['exec', container, 'cat', '/sys/fs/cgroup/cpuacct/cpuacct.usage']);
    return Number(v1.trim()) / 1000;
  } catch {
    return null;
  }
}

/** CPU time the containers have used since they started, in seconds (null: a counter is unreadable). */
export async function cpuSecondsNow(containers: string[]): Promise<number | null> {
  let total = 0;
  for (const c of containers) {
    const us = await cpuUsec(c);
    if (us === null) return null;
    total += us / 1e6;
  }
  return total;
}

/** The container's CPU limit: pinned cores, a CPU quota, or none. */
async function cpuLimit(container: string): Promise<string> {
  try {
    const [cpuset, nano] = (await docker(['inspect', '--format', '{{.HostConfig.CpusetCpus}}|{{.HostConfig.NanoCpus}}', container])).trim().split('|');
    const parts = [];
    if (cpuset) parts.push(`cores ${cpuset}`);
    if (Number(nano) > 0) parts.push(`${Number(nano) / 1e9} CPUs`);
    return parts.join(', ') || 'none';
  } catch {
    return '?';
  }
}

/** The containers that are running, among those asked for; warns about the others. */
export async function checkContainers(engine: string, containers: string[]): Promise<string[]> {
  try {
    await docker(['version', '--format', '{{.Server.Version}}']);
  } catch {
    console.log(`  (resources: docker is not reachable, CPU and memory of ${engine} not measured)`);
    return [];
  }
  const ok: string[] = [];
  for (const c of containers) {
    try {
      if ((await docker(['inspect', '--format', '{{.State.Running}}', c])).trim() === 'true') ok.push(c);
      else console.log(`  (resources: container ${c} is not running, left out)`);
    } catch {
      console.log(`  (resources: no container named ${c}, left out — see --${engine}-containers)`);
    }
  }
  return ok;
}

export class ResourceMonitor {
  private samples: Record<string, Sample[]> = {};
  private idle: Record<string, Sample> = {};
  private cpuStart: Record<string, number | null> = {};
  private cpuLimits: Record<string, string> = {};
  private t0 = 0;
  private running = false;
  private loop?: Promise<void>;

  constructor(readonly engine: string, readonly containers: string[]) {
    for (const c of containers) this.samples[c] = [];
  }

  async start() {
    this.idle = await sample(this.containers).catch(() => ({}));
    for (const c of this.containers) {
      this.cpuStart[c] = await cpuUsec(c);
      this.cpuLimits[c] = await cpuLimit(c);
    }
    this.t0 = Date.now();
    this.running = true;
    this.loop = (async () => {
      while (this.running) {
        try {
          const s = await sample(this.containers);
          for (const c of this.containers) if (s[c]) this.samples[c].push(s[c]);
        } catch { /* a missed sample is not worth stopping the run */ }
      }
    })();
  }

  async stop(): Promise<EngineUsage> {
    this.running = false;
    await this.loop;
    const durationS = (Date.now() - this.t0) / 1000;
    const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : NaN);
    const max = (xs: number[]) => (xs.length ? Math.max(...xs) : NaN);

    const containers: ContainerUsage[] = [];
    for (const c of this.containers) {
      const end = await cpuUsec(c);
      const start = this.cpuStart[c];
      const cpuSeconds = start !== null && end !== null ? (end - start) / 1e6 : null;
      const ss = this.samples[c];
      containers.push({
        container: c,
        cpuSeconds,
        cpuAvgPct: cpuSeconds !== null ? (cpuSeconds / durationS) * 100 : avg(ss.map(s => s.cpuPct)),
        cpuPeakPct: max(ss.map(s => s.cpuPct)),
        memIdleMiB: this.idle[c]?.memMiB ?? NaN,
        memAvgMiB: avg(ss.map(s => s.memMiB)),
        memPeakMiB: max(ss.map(s => s.memMiB)),
        memLimitMiB: this.idle[c]?.limitMiB ?? ss[0]?.limitMiB ?? NaN,
        cpuLimit: this.cpuLimits[c] ?? '?',
      });
    }

    // Totals: peaks are summed sample by sample, so the total peak is the
    // highest combined value actually seen, not a sum of separate peaks.
    const n = Math.min(...this.containers.map(c => this.samples[c].length));
    const combined = (pick: (s: Sample) => number) =>
      Array.from({ length: n }, (_, i) => this.containers.reduce((sum, c) => sum + pick(this.samples[c][i]), 0));
    const cpuKnown = containers.every(c => c.cpuSeconds !== null);
    const cpuSeconds = cpuKnown ? containers.reduce((s, c) => s + (c.cpuSeconds ?? 0), 0) : null;
    return {
      engine: this.engine,
      durationS,
      samples: n,
      containers,
      total: {
        cpuSeconds,
        cpuAvgPct: cpuSeconds !== null ? (cpuSeconds / durationS) * 100 : containers.reduce((s, c) => s + c.cpuAvgPct, 0),
        cpuPeakPct: max(combined(s => s.cpuPct)),
        memIdleMiB: containers.reduce((s, c) => s + c.memIdleMiB, 0),
        memAvgMiB: avg(combined(s => s.memMiB)),
        memPeakMiB: max(combined(s => s.memMiB)),
      },
    };
  }
}

/** The "Resources" section of the report. */
export function resourcesReport(usages: EngineUsage[]): string {
  if (usages.length === 0) return '';
  const f = (v: number | null, digits = 0) => (v === null || Number.isNaN(v) ? '—' : v.toFixed(digits));
  const pad = (s: string, w: number) => s.padStart(w);
  const lines = [
    '── Resources (Docker) ' + '─'.repeat(52),
    `  ${'container'.padEnd(22)}${pad('run', 8)}${pad('CPU time', 10)}${pad('CPU avg', 9)}${pad('CPU peak', 10)}${pad('mem idle', 10)}${pad('mem avg', 9)}${pad('mem peak', 10)}  limits (CPU, memory)`,
  ];
  const row = (label: string, run: string, u: Omit<ContainerUsage, 'container' | 'memLimitMiB' | 'cpuLimit'>, limit = '') =>
    `  ${label.padEnd(22)}${pad(run, 8)}${pad(f(u.cpuSeconds, 1) + ' s', 10)}${pad(f(u.cpuAvgPct) + ' %', 9)}${pad(f(u.cpuPeakPct) + ' %', 10)}` +
    `${pad(f(u.memIdleMiB) + ' MiB', 10)}${pad(f(u.memAvgMiB) + ' MiB', 9)}${pad(f(u.memPeakMiB) + ' MiB', 10)}${limit ? '  ' + limit : ''}`;
  for (const u of usages) {
    lines.push(row(`${u.engine} (total)`, `${f(u.durationS)} s`, u.total));
    for (const c of u.containers) lines.push(row(`  ${c.container}`, '', c, `${c.cpuLimit}, ${f(c.memLimitMiB)} MiB`));
  }
  const [a, b] = [usages.find(u => u.engine === 'camunda'), usages.find(u => u.engine === 'bpmngo')];
  if (a && b) {
    const ratio = (x: number | null, y: number | null) => (x && y ? `×${(y / x).toFixed(2)}` : '—');
    lines.push('');
    lines.push(`  bpmngo / camunda: CPU time ${ratio(a.total.cpuSeconds, b.total.cpuSeconds)}, ` +
      `memory peak ${ratio(a.total.memPeakMiB, b.total.memPeakMiB)}, memory idle ${ratio(a.total.memIdleMiB, b.total.memIdleMiB)}, ` +
      `run time ${ratio(a.durationS, b.durationS)}`);
  }
  lines.push('');
  lines.push('  CPU in % of one core. CPU time from each container\'s cgroup counter over the run; peaks and');
  lines.push('  memory from `docker stats` samples (one every ~2 s). Each engine is measured during its own run.');
  return lines.join('\n');
}
