// internal/service/process_lifecycle_service.go
package service

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/eventlog"
	"github.com/jeremielodi/bpmngo/internal/events"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// ErrProcessInstanceNotFound lets callers (REST controllers, the gRPC
// gateway) distinguish a missing instance from other termination errors via
// errors.Is, matching the ErrJobNotFound/ErrDeployPermissionDenied pattern
// used elsewhere in this package.
var ErrProcessInstanceNotFound = fmt.Errorf("process instance not found")

// TerminateProcessInstance soft-deletes a running process instance —
// extracted from what used to be the entire inline body of
// internal/api/process_instance_controller.go's TerminateProcess handler,
// now shared with the gRPC gateway's CancelProcessInstance RPC. Marks the
// instance (and everything still open under it) terminated instead of
// hard-deleting, cancels open tasks/jobs/timers, then best-effort archives
// into historic_* tables — see internal/service/archive_service.go. A
// failed archive leaves the instance live (already safely marked
// terminated above), never risking data loss.
func TerminateProcessInstance(db *sqlx.DB, dispatcher *events.TaskEventDispatcher, instanceID uuid.UUID, reason string) error {
	return closeProcessInstance(db, dispatcher, instanceID, "terminated", reason, map[string]interface{}{
		"terminated":        true,
		"terminatedAt":      time.Now(),
		"terminationReason": reason,
	})
}

// EndProcessInstance closes a running instance as completed, wherever its
// tokens are (POST /process-instance/:id/end and /stop): the same cleanup
// as TerminateProcessInstance — open tasks, executions, jobs and timers
// cancelled, called instances terminated, then archived — with status
// status ("completed" or "terminated") and variables merged in first.
func EndProcessInstance(db *sqlx.DB, dispatcher *events.TaskEventDispatcher, instanceID uuid.UUID, status, reason string, variables map[string]interface{}) error {
	return closeProcessInstance(db, dispatcher, instanceID, status, reason, variables)
}

func closeProcessInstance(db *sqlx.DB, dispatcher *events.TaskEventDispatcher, instanceID uuid.UUID, status, reason string, variables map[string]interface{}) error {
	processInstanceRepo := repository.NewProcessInstanceRepository(db)
	taskRepo := repository.NewTaskRepository(db, dispatcher)
	engineRepo := repository.NewEngineRepository(db, dispatcher)
	auditService := NewAuditService(db)

	// FindByID answers (nil, nil) for an instance no longer in the live
	// table (already archived).
	instance, err := processInstanceRepo.FindByID(instanceID)
	if err != nil || instance == nil {
		return fmt.Errorf("%w: %s", ErrProcessInstanceNotFound, instanceID)
	}
	// Guard against a real race: an instance that completes normally right
	// before (or concurrently with) an explicit terminate call would
	// otherwise still be found here (its row isn't removed until
	// ArchiveAndDeleteProcessInstance runs), and this function would both
	// overwrite its correct 'completed' status with 'terminated' and write
	// termination audit log rows AFTER the normal-completion path's own
	// archive already ran and deleted everything for this instance —
	// producing permanently orphaned audit_logs rows (found via direct
	// testing while verifying Citus phase 2's dropped
	// audit_logs.process_instance_id FK, which used to turn this exact
	// race into a loud, non-orphaning FK-violation error instead — see
	// architecture/citus-distributed-storage-design.md). Mirrors
	// ArchiveAndDeleteProcessInstance's own existing status guard.
	if instance.Status == "completed" || instance.Status == "terminated" {
		return fmt.Errorf("%w: %s (already %s)", ErrProcessInstanceNotFound, instanceID, instance.Status)
	}

	// Merged into the instance's variables: UpsertVariables replaces the
	// whole set, and the business data must survive into history.
	merged, err := processInstanceRepo.GetVariables(instanceID)
	if err != nil || merged == nil {
		merged = map[string]interface{}{}
	}
	for k, v := range variables {
		merged[k] = v
	}
	if err := processInstanceRepo.UpsertVariables(instanceID, merged); err != nil {
		fmt.Printf("Warning: Failed to add %s variables: %v\n", status, err)
	}

	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	if err := processInstanceRepo.UpdateProcessInstanceStatusTx(tx, instanceID, status, &now); err != nil {
		return err
	}

	// Explicitly clean up everything a hard-delete's FK cascade used to
	// clean up implicitly, so nothing is left looking open/active/pending
	// for a process that no longer runs.
	cancelledTaskIDs, err := taskRepo.CancelOpenTasksByProcessInstanceTx(tx, instanceID)
	if err != nil {
		return err
	}
	if err := processInstanceRepo.TerminateActiveExecutionsByProcessInstanceTx(tx, instanceID); err != nil {
		return err
	}
	cancelledJobIDs, err := engineRepo.CancelPendingJobsByProcessInstanceTx(tx, instanceID)
	if err != nil {
		return err
	}
	if err := engineRepo.DeleteTimersByProcessInstanceTx(tx, instanceID); err != nil {
		return err
	}
	if err := repository.NewEventSubscriptionRepository(db).ConsumeByProcessInstanceTx(tx, instanceID); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	completed := status == "completed"
	if completed {
		LogAuditErr("process completed", auditService.LogProcessCompleted(instanceID, variables))
	} else {
		LogAuditErr("process terminated", auditService.LogProcessTerminated(instanceID, reason))
	}
	for _, taskID := range cancelledTaskIDs {
		LogAuditErr("task cancelled", auditService.LogTaskCancelled(taskID, instanceID, "process "+status))
	}
	for _, jobID := range cancelledJobIDs {
		LogAuditErr("job cancelled", auditService.LogJobFailed(jobID, instanceID, "cancelled: process "+status, 0))
	}
	LogAuditErr("timers cancelled", auditService.LogTimerCancelled(instanceID, nil))

	if p := eventlog.Global(); p != nil {
		if completed {
			p.ProcessCompleted(instanceID.String())
		} else {
			p.ProcessTerminated(instanceID.String(), reason)
		}
	}

	// The processes its call activities started go with it.
	TerminateOrphanedCalledInstances(db, dispatcher, &instanceID)

	var deleteReason *string
	if !completed {
		deleteReason = &reason
	}
	if err := ArchiveFinishedInstance(db, instanceID, deleteReason); err != nil {
		fmt.Printf("Warning: failed to archive %s process instance %s: %v\n", status, instanceID, err)
	}

	return nil
}
