// internal/service/job_service.go
package service

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/common"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/eventlog"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/metrics"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// ErrJobNotFound and ErrJobInvalidState let callers (REST controllers,
// the gRPC gateway) distinguish these two cases from a generic failure
// via errors.Is, while still getting a human-readable message from
// err.Error().
var (
	ErrJobNotFound     = fmt.Errorf("job not found")
	ErrJobInvalidState = fmt.Errorf("job is not in a lockable state")
	// ErrJobFinished is the ErrJobInvalidState of a job already completed
	// or cancelled: one that no longer exists, for Camunda 8.
	ErrJobFinished = fmt.Errorf("%w (job no longer exists)", ErrJobInvalidState)
)

type CompleteJobResult struct {
	ProcessInstanceID uuid.UUID
	ProcessCompleted  bool
	// Graph/ExecutionID are set when ProcessCompleted is false — the
	// caller must resume execution itself (runtime.NewRuntime(Graph, db,
	// dispatcher).ExecuteExecution(ctx, ExecutionID)) since internal/
	// runtime already imports internal/service, so this package cannot
	// call into runtime directly without an import cycle. This mirrors
	// StartProcessInstance, which leaves execution to its callers too.
	Graph       *engine.ProcessGraph
	ExecutionID int64
	// ExecutionWaiting means the same transaction already created the next
	// service-task job, so the caller must not resume the runtime again.
	ExecutionWaiting bool
	NextJobID        int64
	NextJobType      string
}

// CompleteJob completes a locked external task/job — extracted from what
// used to be the entire inline body of
// internal/api/external_task_controller.go's CompleteTask handler, now
// shared with the gRPC gateway's CompleteJob RPC. variables is already
// normalized to simple (non-typed) map[string]interface{} form.
func CompleteJob(db *sqlx.DB, jobID int64, variables map[string]interface{}) (*CompleteJobResult, error) {
	externalTaskRepo := repository.NewExternalTaskRepository(db)
	auditService := NewAuditService(db)

	tx, err := db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	txStarted, committed := time.Now(), false
	defer func() { metrics.ObserveEngineTransaction("complete_job", txStarted, committed, "operation_error") }()

	jobWithExec, err := externalTaskRepo.GetJobWithExecutionTx(tx, jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch job details: %w", err)
	}
	if jobWithExec == nil {
		return nil, ErrJobNotFound
	}
	if jobWithExec.Status != "locked" {
		if jobWithExec.Status == "completed" || jobWithExec.Status == "cancelled" {
			return nil, fmt.Errorf("%w: current status %s", ErrJobFinished, jobWithExec.Status)
		}
		return nil, fmt.Errorf("%w: current status %s", ErrJobInvalidState, jobWithExec.Status)
	}

	processInstanceID := jobWithExec.ProcessInstanceID
	processInstanceKey := jobWithExec.ProcessInstanceKey
	executionID := jobWithExec.ExecutionID
	currentElementID := jobWithExec.CurrentElementID

	var graph engine.ProcessGraph
	if err := json.Unmarshal(jobWithExec.GraphJSON, &graph); err != nil {
		return nil, fmt.Errorf("failed to parse process graph: %w", err)
	}
	currentNode, err := externalTaskRepo.GetNodeByID(&graph, currentElementID)
	if err != nil {
		return nil, fmt.Errorf("current node not found: %w", err)
	}

	// The job's variables, with the activity's output mappings, as in
	// Camunda 8.
	if err := ApplyActivityOutputTx(tx, processInstanceID, executionID, currentNode, variables); err != nil {
		return nil, fmt.Errorf("failed to update variables: %w", err)
	}

	if err := externalTaskRepo.CompleteJobByKeyTx(tx, processInstanceKey, jobID); err != nil {
		return nil, fmt.Errorf("failed to complete job: %w", err)
	}

	// Where the token goes next. Without an outgoing flow it ends in the
	// activity's scope (engine.ImplicitEnd), and the runtime takes it from
	// there — ending a sub-process, an iteration of a multi-instance
	// activity (the runtime notices an inner instance leaving its
	// activity), or the process.
	var nextNodeID string
	switch currentNode.Type {
	case engine.ServiceTaskType:
		if nextNodeID, err = common.ResolveNext(currentNode, nil); err != nil {
			return nil, err
		}

	case engine.BoundaryTimerEventType:
		if nextNodeID, err = common.ResolveNext(currentNode, nil); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("node %s is not a service task or boundary event, type: %s", currentNode.ID, currentNode.Type)
	}

	// The job's activity is left: its boundary events can no longer fire,
	// and it can now be compensated.
	completionRepo := repository.NewEngineRepository(db, nil)
	if err := completionRepo.DisarmBoundaryTriggersTx(tx, processInstanceID, executionID); err != nil {
		return nil, fmt.Errorf("failed to disarm boundary events: %w", err)
	}
	if currentNode.CompensationHandler != "" {
		if err := completionRepo.RecordCompensableTx(tx, processInstanceID, executionID, currentNode.ID, currentNode.CompensationHandler); err != nil {
			return nil, fmt.Errorf("failed to record compensation: %w", err)
		}
	}
	// Fast unit-of-work: for a plain service-task successor, create its job
	// in this same transaction. Complex activities keep the general runtime
	// path, which arms boundaries, handles local input scopes and MI bodies.
	// An iteration of a multi-instance activity never takes it: leaving the
	// activity ends the iteration, the body moves on.
	nextNode := graph.Nodes[nextNodeID]
	fastNext := currentNode.MultiInstance == nil &&
		nextNode != nil && nextNode.Type == engine.ServiceTaskType && nextNode.JobType != nil &&
		nextNode.MultiInstance == nil && len(nextNode.InputMappings) == 0 && !hasAttachedBoundary(&graph, nextNode.ID)
	var nextJobID int64
	if fastNext {
		visible, err := repository.VariablesForExecutionByKey(tx, processInstanceKey, executionID)
		if err != nil {
			return nil, fmt.Errorf("failed to load next job variables: %w", err)
		}
		payload, err := json.Marshal(visible)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal next job variables: %w", err)
		}
		nextJobID = ids.NewEngineKey()
		if err := completionRepo.CreateJobByKeyTx(tx, nextJobID, processInstanceKey, executionID, *nextNode.JobType, payload); err != nil {
			return nil, fmt.Errorf("failed to create next job: %w", err)
		}
		if err := completionRepo.UpdateExecutionStatusAndNodeByKeyTx(tx, processInstanceKey, executionID, "waiting", nextNodeID); err != nil {
			return nil, err
		}
		if err := repository.NotifyJobAvailableTx(tx, *nextNode.JobType); err != nil {
			return nil, fmt.Errorf("failed to queue next job notification: %w", err)
		}
	} else if err := externalTaskRepo.UpdateExecutionCurrentNodeByKeyTx(tx, processInstanceKey, executionID, nextNodeID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	LogAuditErr("job completed", auditService.LogJobCompleted(jobID, processInstanceID, variables))
	// JOB_COMPLETED already carries the result variables. A second
	// VARIABLES_MERGED row for the same write duplicated both semantics and
	// JSON/WAL; standalone SetVariables and other merge sources remain audited.
	LogAuditErr("execution moved", auditService.LogExecutionMoved(executionID, processInstanceID, currentElementID, nextNodeID, variables))
	if fastNext {
		LogAuditErr("job created", auditService.LogJobCreated(processInstanceID, nextJobID, *nextNode.JobType))
	}

	if p := eventlog.Global(); p != nil {
		p.JobCompleted(processInstanceID.String(), strconv.FormatInt(jobID, 10))
		p.ExecutionMoved(processInstanceID.String(), strconv.FormatInt(executionID, 10), currentElementID, nextNodeID)
		if fastNext {
			p.JobCreated(processInstanceID.String(), strconv.FormatInt(nextJobID, 10), strconv.FormatInt(executionID, 10), *nextNode.JobType)
		}
	}

	result := &CompleteJobResult{ProcessInstanceID: processInstanceID, ProcessCompleted: false, Graph: &graph, ExecutionID: executionID}
	if fastNext {
		result.ExecutionWaiting = true
		result.NextJobID = nextJobID
		result.NextJobType = *nextNode.JobType
	}
	return result, nil
}

func hasAttachedBoundary(graph *engine.ProcessGraph, activityID string) bool {
	for _, node := range graph.Nodes {
		if node.AttachedToRef == activityID {
			return true
		}
	}
	return false
}

type FailJobResult struct {
	PermanentlyFailed bool
	BoundaryTriggered bool
	// Graph/ExecutionID are set when BoundaryTriggered is true — the
	// caller should resume execution itself (see CompleteJobResult for
	// why: avoiding an internal/runtime <-> internal/service import
	// cycle). Unlike CompleteJob, a resume failure here is non-fatal —
	// the failure was already durably recorded, so log and move on,
	// matching the original inline behavior.
	Graph       *engine.ProcessGraph
	ExecutionID int64
	// PropagateError is set when the BPMN error is caught by a call
	// activity above ProcessInstanceID: the caller must hand it up with
	// runtime.PropagateErrorToCaller (no incident was raised).
	PropagateError    bool
	ProcessInstanceID uuid.UUID
}

// FailJob records a job failure — extracted from what used to be the
// entire inline body of
// internal/api/external_task_controller.go's HandleFailure handler, now
// shared with the gRPC gateway's FailJob RPC.
//
// retriesRemaining is the number of retries to leave AFTER this failure
// (Zeebe's own convention) — callers using Camunda 7's "current retry
// count" convention must decrement before calling this (see
// ExternalTaskController.HandleFailure, which does req.Retries-1 before
// calling this so its own REST semantics are unchanged).
func FailJob(db *sqlx.DB, jobID int64, retriesRemaining int, errorMessage, errorCode string) (*FailJobResult, error) {
	return FailJobWithBackoff(db, jobID, retriesRemaining, errorMessage, errorCode, 0)
}

// FailJobWithBackoff is FailJob with a delay before a retried job can be
// fetched again (Camunda 7 retryTimeout, Camunda 8 retryBackOff).
func FailJobWithBackoff(db *sqlx.DB, jobID int64, retriesRemaining int, errorMessage, errorCode string, retryBackoff time.Duration) (*FailJobResult, error) {
	externalTaskRepo := repository.NewExternalTaskRepository(db)
	incidentRepo := repository.NewIncidentRepository(db)
	auditService := NewAuditService(db)

	tx, err := db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Load job context up front — needed for audit logging on every
	// branch, as well as the boundary-check/incident-creation below.
	jobCtxEarly, jobCtxErr := externalTaskRepo.GetJobWithExecutionTx(tx, jobID)
	if jobCtxErr != nil {
		return nil, fmt.Errorf("failed to fetch job details: %w", jobCtxErr)
	}
	if jobCtxEarly == nil {
		return nil, ErrJobNotFound
	}
	processInstanceID := jobCtxEarly.ProcessInstanceID

	result := &FailJobResult{}

	if retriesRemaining <= 0 {
		result.PermanentlyFailed = true

		if err := externalTaskRepo.FailJobPermanentlyTx(tx, processInstanceID, jobID, errorMessage, errorCode); err != nil {
			return nil, fmt.Errorf("failed to mark job as failed: %w", err)
		}

		jobCtx, jobErr := jobCtxEarly, jobCtxErr

		// If an error code was provided, check for a matching error boundary event.
		if errorCode != "" && jobErr == nil && jobCtx != nil {
			if len(jobCtx.GraphJSON) > 0 {
				var graph engine.ProcessGraph
				if jsonErr := json.Unmarshal(jobCtx.GraphJSON, &graph); jsonErr == nil {
					node, scope := graph.FindErrorCatcher(jobCtx.CurrentElementID, errorCode)
					if node != nil && node.Type == engine.EventSubProcessType {
						// Caught by an error event sub-process: the caller
						// runs its trigger execution, which interrupts the
						// scope (this job's execution included) and starts
						// the event sub-process.
						engineRepo := repository.NewEngineRepository(db, nil)
						triggerID, trigErr := engineRepo.CreateErrorTriggerTx(tx, jobCtx.ProcessInstanceID, jobCtx.ExecutionID, node.ID, scope)
						if trigErr != nil {
							log.Printf("error event sub-process %s: %v", node.ID, trigErr)
						} else if commitErr := tx.Commit(); commitErr == nil {
							LogAuditErr("job failed", auditService.LogJobFailed(jobID, jobCtx.ProcessInstanceID, errorMessage, 0))
							if p := eventlog.Global(); p != nil {
								p.JobFailed(jobCtx.ProcessInstanceID.String(), strconv.FormatInt(jobID, 10), errorMessage, 0, false)
							}
							result.BoundaryTriggered = true
							result.Graph = &graph
							result.ExecutionID = triggerID
							return result, nil
						} else {
							return nil, fmt.Errorf("failed to commit transaction: %w", commitErr)
						}
					} else if node != nil {
						// Caught on the task itself, the job's execution
						// moves to the boundary. Caught on an enclosing
						// sub-process, the execution parked on that
						// sub-process moves instead, and everything still
						// running inside it is interrupted.
						catchingExecID, fromNodeID := jobCtx.ExecutionID, jobCtx.CurrentElementID
						var scopeTasks, scopeJobs []int64
						var scopeErr error
						if scope != "" {
							engineRepo := repository.NewEngineRepository(db, nil)
							catchingExecID, scopeErr = engineRepo.FindScopeExecutionTx(tx, jobCtx.ProcessInstanceID, jobCtx.ExecutionID, scope)
							if scopeErr == nil {
								fromNodeID = scope
								scopeTasks, scopeJobs, scopeErr = engineRepo.CancelScopeTx(tx, jobCtx.ProcessInstanceID, catchingExecID, 0)
							}
						}
						if scopeErr == nil {
							// The catching activity is left through its error
							// boundary: its other boundary events are disarmed.
							scopeErr = repository.NewEngineRepository(db, nil).DisarmBoundaryTriggersTx(tx, jobCtx.ProcessInstanceID, catchingExecID)
						}
						if scopeErr != nil {
							log.Printf("error boundary %s on sub-process %s: %v", node.ID, scope, scopeErr)
						} else if _, execErr := tx.Exec(`
							UPDATE public.executions
							SET current_element_id = $1, status = 'active', updated_at = NOW()
							WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $2)
							  AND engine_key = $3
						`, node.ID, jobCtx.ProcessInstanceID, catchingExecID); execErr == nil {
							if commitErr := tx.Commit(); commitErr == nil {
								LogAuditErr("job failed", auditService.LogJobFailed(jobID, jobCtx.ProcessInstanceID, errorMessage, 0))
								for _, taskID := range scopeTasks {
									LogAuditErr("task cancelled", auditService.LogTaskCancelled(taskID, jobCtx.ProcessInstanceID, "sub-process interrupted by error boundary"))
								}
								for _, cancelledJobID := range scopeJobs {
									LogAuditErr("job cancelled", auditService.LogJobFailed(cancelledJobID, jobCtx.ProcessInstanceID, "cancelled: sub-process interrupted by error boundary", 0))
								}
								LogAuditErr("execution moved", auditService.LogExecutionMoved(catchingExecID, jobCtx.ProcessInstanceID, fromNodeID, node.ID, nil))
								if p := eventlog.Global(); p != nil {
									p.JobFailed(jobCtx.ProcessInstanceID.String(), strconv.FormatInt(jobID, 10), errorMessage, 0, false)
									p.ExecutionMoved(jobCtx.ProcessInstanceID.String(), strconv.FormatInt(catchingExecID, 10), fromNodeID, node.ID)
								}
								result.BoundaryTriggered = true
								result.Graph = &graph
								result.ExecutionID = catchingExecID
							}
							return result, nil
						}
					}
				}
			}
		}

		// Nothing in this instance catches the error: a call activity that
		// started it (or one further up) may — the caller then hands it up
		// (runtime.PropagateErrorToCaller). Otherwise, an incident.
		if errorCode != "" && jobErr == nil && jobCtx != nil && CallerCatchesError(tx, jobCtx.ProcessInstanceID, errorCode) {
			result.PropagateError = true
			result.ProcessInstanceID = jobCtx.ProcessInstanceID
		} else if jobErr == nil && jobCtx != nil {
			incidentType := models.IncidentJobNoRetries
			if errorCode != "" {
				incidentType = models.IncidentUnhandledErrorEvent // a BPMN error nothing caught
			}
			inc := &models.IncidentCreate{
				ProcessInstanceID: jobCtx.ProcessInstanceID,
				JobID:             &jobID,
				IncidentType:      incidentType,
				ActivityID:        jobCtx.CurrentElementID,
				ErrorMessage:      errorMessage,
				ErrorCode:         errorCode,
			}
			if _, err := incidentRepo.CreateIncidentTx(tx, inc); err != nil {
				log.Printf("failed to create incident: %v", err)
			}
		}
	} else {
		if err := externalTaskRepo.ResetJobForRetryTx(tx, processInstanceID, jobID, retriesRemaining, errorMessage, retryBackoff); err != nil {
			return nil, fmt.Errorf("failed to reset job for retry: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	if jobCtxErr == nil && jobCtxEarly != nil {
		LogAuditErr("job failed", auditService.LogJobFailed(jobID, jobCtxEarly.ProcessInstanceID, errorMessage, retriesRemaining))
		if p := eventlog.Global(); p != nil {
			// incidentCreated is approximated as "permanently failed" here —
			// good enough for Phase 0 shadow verification; the exact incident
			// row's success/failure isn't tracked separately above.
			p.JobFailed(jobCtxEarly.ProcessInstanceID.String(), strconv.FormatInt(jobID, 10), errorMessage, int32(retriesRemaining), result.PermanentlyFailed)
		}
	}

	return result, nil
}
