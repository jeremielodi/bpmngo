// service/archive_service.go
package service

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/metrics"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// ArchiveAndDeleteProcessInstance copies a finished (completed/terminated)
// process instance's data into the historic_* tables, then deletes its live
// rows (process_instances, cascading to executions/tasks/variables/jobs/
// timer_jobs/audit_logs). Runs in its own transaction, separate from the
// transaction that marked the instance finished — if any step here fails,
// the transaction rolls back and the live row is left exactly as-is
// (already safely completed/terminated), so a failed archive never risks
// losing data. Callers should log a failure but not treat it as fatal.
func ArchiveAndDeleteProcessInstance(db *sqlx.DB, instanceID uuid.UUID, deleteReason *string) error {
	return archiveAndDeleteProcessInstance(db, instanceID, deleteReason, true)
}

// archiveAndDeleteProcessInstance optionally skips the audit flush. The
// asynchronous archiver flushes once before processing its whole batch;
// synchronous and direct callers retain the reader-consistency guarantee.
func archiveAndDeleteProcessInstance(db *sqlx.DB, instanceID uuid.UUID, deleteReason *string, flushAudit bool) error {
	if flushAudit {
		repository.FlushAuditLogs()
	}
	snapshot, err := prepareArchiveSnapshot(db, instanceID, deleteReason)
	if err != nil {
		return err
	}
	return writeArchiveSnapshots(db, []archiveSnapshot{*snapshot})
}

type archiveSnapshot struct {
	instanceID uuid.UUID
	process    repository.HistoricProcessInstance
	activities []repository.HistoricActivityInstanceRow
	tasks      []repository.HistoricTaskRow
	incidents  []repository.HistoricIncidentRow
	variables  map[string]interface{}
	endedAt    time.Time
}

// prepareArchiveSnapshot performs the read/transform side without holding a
// write transaction. The background archiver can prepare several instances
// and commit them together.
func prepareArchiveSnapshot(db *sqlx.DB, instanceID uuid.UUID, deleteReason *string) (*archiveSnapshot, error) {
	instanceRepo := repository.NewProcessInstanceRepository(db)
	taskRepo := repository.NewTaskRepository(db, nil)
	auditRepo := repository.NewAuditRepository(db)
	incidentRepo := repository.NewIncidentRepository(db)

	instance, err := instanceRepo.FindByID(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load process instance: %w", err)
	}
	if instance == nil {
		return nil, fmt.Errorf("process instance not found")
	}
	if instance.Status != "completed" && instance.Status != "terminated" {
		return nil, fmt.Errorf("refusing to archive a process instance with status %q (must be completed or terminated)", instance.Status)
	}
	if instance.EndedAt == nil {
		return nil, fmt.Errorf("process instance has no ended_at, cannot compute archive duration")
	}

	tasks, err := taskRepo.FindTasksByProcessInstance(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load tasks: %w", err)
	}
	variables, err := instanceRepo.GetVariables(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load variables: %w", err)
	}
	var logs []models.AuditLog
	logs, err = auditRepo.GetProcessAuditLogsFlushed(instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to load audit logs: %w", err)
	}
	incidents, err := incidentRepo.List(repository.IncidentFilter{ProcessInstanceID: &instanceID})
	if err != nil {
		return nil, fmt.Errorf("failed to load incidents: %w", err)
	}

	steps := BuildTokenHistory(logs, tasks)
	activityRows := make([]repository.HistoricActivityInstanceRow, len(steps))
	for i, step := range steps {
		activityRows[i] = repository.HistoricActivityInstanceRow{
			ProcessInstanceID: instanceID,
			ExecutionID:       step.ExecutionId,
			Action:            step.Action,
			ElementID:         nilIfEmpty(step.ElementId),
			ElementName:       nilIfEmpty(step.ElementName),
			TaskID:            step.TaskId,
			Detail:            nilIfEmpty(step.Detail),
			OccurredAt:        step.Timestamp,
		}
	}

	incidentRows := make([]repository.HistoricIncidentRow, len(incidents))
	for i, inc := range incidents {
		row := repository.HistoricIncidentRow{
			ID:                   inc.ID,
			ProcessInstanceID:    instanceID,
			ProcessDefinitionKey: instance.ProcessKey,
			JobID:                inc.JobID,
			IncidentType:         inc.IncidentType,
			ActivityID:           inc.ActivityID,
			State:                inc.State,
			CreatedAt:            inc.CreatedAt,
		}
		if inc.ErrorMessage.Valid {
			row.ErrorMessage = &inc.ErrorMessage.String
		}
		if inc.ErrorCode.Valid {
			row.ErrorCode = &inc.ErrorCode.String
		}
		if inc.ResolvedAt.Valid {
			row.ResolvedAt = &inc.ResolvedAt.Time
		}
		incidentRows[i] = row
	}

	taskRows := make([]repository.HistoricTaskRow, len(tasks))
	for i, t := range tasks {
		taskRows[i] = repository.HistoricTaskRow{
			ID:                t.ID,
			ProcessInstanceID: instanceID,
			TaskDefinitionKey: t.TaskDefinitionKey,
			TaskName:          nilIfEmpty(t.TaskName),
			Assignee:          t.Assignee,
			Status:            t.Status,
			Priority:          &t.Priority,
			CreatedAt:         t.CreatedAt,
			ClaimedAt:         t.ClaimedAt,
			CompletedAt:       t.CompletedAt,
		}
	}

	durationMillis := instance.EndedAt.Sub(instance.StartedAt).Milliseconds()
	var version *int
	if instance.Version > 0 {
		v := instance.Version
		version = &v
	}

	return &archiveSnapshot{
		instanceID: instanceID,
		process: repository.HistoricProcessInstance{
			ID:                       instance.ID,
			ProcessDefinitionID:      instance.ProcessDefinitionID,
			ProcessDefinitionKey:     instance.ProcessKey,
			ProcessDefinitionVersion: version,
			TenantID:                 instance.TenantID,
			StartedBy:                instance.StartedBy,
			StartedAt:                instance.StartedAt,
			EndedAt:                  *instance.EndedAt,
			DurationMillis:           &durationMillis,
			State:                    instance.Status,
			DeleteReason:             deleteReason,
			BusinessKey:              instance.BusinessKey,
		},
		activities: activityRows,
		tasks:      taskRows,
		incidents:  incidentRows,
		variables:  variables,
		endedAt:    *instance.EndedAt,
	}, nil
}

// writeArchiveSnapshots commits several independent finished instances in a
// single transaction. A small caller-side batch bounds lock time and rollback
// scope while removing most archive BEGIN/COMMIT traffic.
func writeArchiveSnapshots(db *sqlx.DB, snapshots []archiveSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	started := time.Now()
	committed := false
	defer func() {
		metrics.ObserveEngineTransaction("archive_batch", started, committed, "archive_error")
	}()
	archiveRepo := repository.NewHistoricArchiveRepository(db)
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	// No need to wait for the disk: if a crash loses this commit, it loses
	// it whole — the instance is still completed in the live tables and
	// gets archived again.
	if _, err := tx.Exec(`SET LOCAL synchronous_commit TO OFF`); err != nil {
		return err
	}

	for _, snapshot := range snapshots {
		if err = archiveRepo.InsertProcessInstanceTx(tx, snapshot.process); err != nil {
			return fmt.Errorf("instance %s: failed to insert historic process instance: %w", snapshot.instanceID, err)
		}
		if err = archiveRepo.InsertActivityInstancesTx(tx, snapshot.activities); err != nil {
			return fmt.Errorf("instance %s: failed to insert historic activity instances: %w", snapshot.instanceID, err)
		}
		if err = archiveRepo.InsertTasksTx(tx, snapshot.instanceID, snapshot.tasks); err != nil {
			return fmt.Errorf("instance %s: failed to insert historic tasks: %w", snapshot.instanceID, err)
		}
		if err = archiveRepo.InsertIncidentsTx(tx, snapshot.incidents); err != nil {
			return fmt.Errorf("instance %s: failed to insert historic incidents: %w", snapshot.instanceID, err)
		}
		if err = archiveRepo.InsertVariablesTx(tx, snapshot.instanceID, snapshot.variables, snapshot.endedAt); err != nil {
			return fmt.Errorf("instance %s: failed to insert historic variables: %w", snapshot.instanceID, err)
		}
		if err = archiveRepo.CleanupOrphanExecutionStateTx(tx, snapshot.instanceID); err != nil {
			return fmt.Errorf("instance %s: failed to clean up orphan execution state: %w", snapshot.instanceID, err)
		}
		if err = archiveRepo.DeleteLiveInstanceTx(tx, snapshot.instanceID); err != nil {
			return fmt.Errorf("instance %s: failed to delete live process instance: %w", snapshot.instanceID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit archive transaction: %w", err)
	}
	committed = true
	metrics.ArchiveBatchSize.Observe(float64(len(snapshots)))
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
