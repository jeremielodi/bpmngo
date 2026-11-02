#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  echo "usage: $0 OUTPUT_DIR POSTGRES_CONTAINER" >&2
  exit 2
fi

out=$1
container=$2
mkdir -p "$out"

psql_csv() {
  file=$1
  query=$2
  docker exec "$container" psql -X -U goflow -d goflow --csv -c "$query" > "$out/$file.csv"
}

date -u +%Y-%m-%dT%H:%M:%SZ > "$out/captured-at.txt"

psql_csv wal "SELECT * FROM pg_stat_wal"
psql_csv bgwriter "SELECT * FROM pg_stat_bgwriter"
psql_csv database "SELECT datname,numbackends,xact_commit,xact_rollback,blks_read,blks_hit,temp_files,temp_bytes,deadlocks,blk_read_time,blk_write_time,session_time,active_time,idle_in_transaction_time,stats_reset FROM pg_stat_database WHERE datname='goflow'"
psql_csv io "SELECT backend_type,object,context,reads,read_time,writes,write_time,writebacks,writeback_time,extends,extend_time,hits,evictions,reuses,fsyncs,fsync_time,stats_reset FROM pg_stat_io ORDER BY backend_type,object,context"
psql_csv tables "SELECT relname,n_live_tup,n_dead_tup,round(100.0*n_dead_tup/GREATEST(n_live_tup+n_dead_tup,1),2) AS dead_pct,pg_total_relation_size(relid) AS total_bytes,seq_scan,idx_scan,n_tup_ins,n_tup_upd,n_tup_del,n_tup_hot_upd,autovacuum_count,autoanalyze_count,last_autovacuum,last_autoanalyze FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC"
psql_csv statements "SELECT calls,round(total_exec_time::numeric,3) AS total_exec_ms,round(mean_exec_time::numeric,6) AS mean_exec_ms,rows,shared_blks_hit,shared_blks_read,shared_blks_dirtied,shared_blks_written,temp_blks_read,temp_blks_written,wal_records,wal_fpi,wal_bytes,left(regexp_replace(query,E'[\\n\\r\\t ]+',' ','g'),500) AS query FROM pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname='goflow') ORDER BY total_exec_time DESC LIMIT 100"
psql_csv wal_by_operation "WITH statements AS (SELECT calls,wal_records,wal_fpi,wal_bytes,lower(query) AS query FROM pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname='goflow')), classified AS (SELECT CASE WHEN query LIKE '%insert into public.audit_logs%' THEN 'audit_insert' WHEN query LIKE '%insert into public.jobs%' OR query LIKE '%update public.jobs%' THEN 'jobs' WHEN query LIKE '%historic_%' OR query LIKE '%delete from public.process_instances%' THEN 'archive_history' WHEN query LIKE '%public.executions%' THEN 'executions' WHEN query LIKE '%public.variables%' OR query LIKE '%public.scope_variables%' THEN 'variables' WHEN query LIKE '%public.process_instances%' THEN 'process_instances' ELSE 'other' END AS operation,calls,wal_records,wal_fpi,wal_bytes FROM statements) SELECT operation,sum(calls) AS calls,sum(wal_records) AS wal_records,sum(wal_fpi) AS wal_fpi,sum(wal_bytes) AS wal_bytes FROM classified GROUP BY operation ORDER BY wal_bytes DESC"
psql_csv activity "SELECT state,wait_event_type,wait_event,count(*) AS sessions,max(EXTRACT(EPOCH FROM (clock_timestamp()-xact_start))) FILTER (WHERE xact_start IS NOT NULL) AS oldest_transaction_seconds FROM pg_stat_activity WHERE datname='goflow' GROUP BY state,wait_event_type,wait_event ORDER BY sessions DESC"
psql_csv partitions "SELECT parent.relname AS parent,child.relname AS partition,pg_get_expr(child.relpartbound,child.oid) AS bounds,pg_total_relation_size(child.oid) AS total_bytes FROM pg_inherits i JOIN pg_class parent ON parent.oid=i.inhparent JOIN pg_class child ON child.oid=i.inhrelid JOIN pg_namespace n ON n.oid=parent.relnamespace WHERE n.nspname='public' ORDER BY parent.relname,child.relname"
