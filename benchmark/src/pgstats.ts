/**
 * pgstats.ts — what BpmnGo's PostgreSQL does during a load level.
 *
 * Camunda keeps its state in its own process; BpmnGo's lives in PostgreSQL,
 * where most of its CPU goes. Read before and after a measured window, the
 * differences of pg_stat_statements, pg_stat_wal and pg_stat_database say
 * where that CPU goes, per completed instance: statements, their execution
 * and planning time, commits, WAL. Read through `docker exec … psql`, so the
 * bench needs no database driver. pg_stat_statements must be loaded
 * (shared_preload_libraries, as BpmnGo's compose file does); without it,
 * only WAL and commits are reported.
 */
import { execFile } from 'child_process';

export interface PgStatement {
  query: string;
  calls: number;
  execMs: number;
  planMs: number;
  walBytes: number;
}

export interface PgLevelStats {
  commits: number;
  walBytes: number;
  statements: { calls: number; execMs: number; planMs: number } | null; // null: pg_stat_statements unavailable
  top: PgStatement[];          // by execution time, over the window
}

interface Snapshot {
  commits: number;
  walBytes: number;
  statements: Map<string, PgStatement> | null;
}

export interface PgTarget { container: string; user: string; db: string }

function psql(t: PgTarget, sql: string): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile('docker', ['exec', t.container, 'psql', '-U', t.user, '-d', t.db, '-AtX', '-F', '\t', '-c', sql],
      { timeout: 30000, windowsHide: true, maxBuffer: 32 * 1024 * 1024 }, (err, stdout) => err ? reject(err) : resolve(stdout));
  });
}

async function snapshot(t: PgTarget): Promise<Snapshot> {
  const [db, wal] = await Promise.all([
    psql(t, `SELECT xact_commit FROM pg_stat_database WHERE datname = current_database()`),
    psql(t, `SELECT wal_bytes FROM pg_stat_wal`),
  ]);
  let statements: Map<string, PgStatement> | null = new Map();
  try {
    const rows = await psql(t, `SELECT queryid, calls, total_exec_time, total_plan_time, wal_bytes,
                                        regexp_replace(left(query, 300), '\\s+', ' ', 'g')
                                 FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`);
    for (const line of rows.split('\n').filter(Boolean)) {
      const [id, calls, exec, plan, walB, query] = line.split('\t');
      statements.set(id, { query, calls: +calls, execMs: +exec, planMs: +plan, walBytes: +walB });
    }
  } catch {
    statements = null;
  }
  return { commits: Number(db.trim()), walBytes: Number(wal.trim()), statements };
}

/** Snapshots PostgreSQL now; the returned function gives the differences since. */
export async function startPgWindow(t: PgTarget): Promise<() => Promise<PgLevelStats>> {
  const before = await snapshot(t);
  return async () => {
    const after = await snapshot(t);
    let statements: PgLevelStats['statements'] = null;
    const top: PgStatement[] = [];
    if (before.statements && after.statements) {
      statements = { calls: 0, execMs: 0, planMs: 0 };
      for (const [id, s] of after.statements) {
        const b = before.statements.get(id);
        const d: PgStatement = {
          query: s.query,
          calls: s.calls - (b?.calls ?? 0),
          execMs: s.execMs - (b?.execMs ?? 0),
          planMs: s.planMs - (b?.planMs ?? 0),
          walBytes: s.walBytes - (b?.walBytes ?? 0),
        };
        if (d.calls <= 0) continue;
        statements.calls += d.calls;
        statements.execMs += d.execMs;
        statements.planMs += d.planMs;
        top.push(d);
      }
      top.sort((a, b) => b.execMs - a.execMs);
    }
    return { commits: after.commits - before.commits, walBytes: after.walBytes - before.walBytes, statements, top: top.slice(0, 10) };
  };
}

/** Checks the container answers and says whether pg_stat_statements is there. */
export async function checkPg(t: PgTarget): Promise<string | null> {
  try {
    const ext = await psql(t, `SELECT count(*) FROM pg_extension WHERE extname = 'pg_stat_statements'`);
    return ext.trim() === '1' ? null : 'pg_stat_statements is not installed: only commits and WAL are reported';
  } catch (e: any) {
    return `cannot query PostgreSQL in container ${t.container}: ${e.message ?? e}`;
  }
}
