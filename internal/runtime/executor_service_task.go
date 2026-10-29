package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jeremielodi/bpmngo/internal/common"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/eventlog"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/metrics"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
)

func (r *Runtime) executeServiceTask(ctx context.Context, engineRepo *repository.EngineRepository, exec *repository.Execution, node *engine.Node, variables map[string]interface{}) error {
	tx, err := r.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	txStarted, committed := time.Now(), false
	defer func() {
		metrics.ObserveEngineTransaction("create_service_job", txStarted, committed, "operation_error")
	}()

	if err := engineRepo.UpdateExecutionStatusAndNodeByKeyTx(tx, exec.ProcessInstanceKey, exec.ID, "active", node.ID); err != nil {
		return fmt.Errorf("failed to update execution: %w", err)
	}

	if node.JobType == nil {
		return fmt.Errorf("service task %s has no job type", node.ID)
	}

	payloadVars := variables
	if mapped := common.EvaluateIOMappings(node.InputMappings, variables); len(mapped) > 0 {
		payloadVars = make(map[string]interface{}, len(variables)+len(mapped))
		for k, v := range variables {
			payloadVars[k] = v
		}
		for k, v := range mapped {
			payloadVars[k] = v
		}
		// Input mappings are the activity's local variables.
		if err := repository.SetLocalVariablesByKeyTx(tx, exec.ProcessInstanceID, exec.ProcessInstanceKey, exec.ID, mapped); err != nil {
			return models.WithIncidentType(models.IncidentIOMappingError, fmt.Errorf("service task %s: input mappings: %w", node.ID, err))
		}
	}

	payload, err := json.Marshal(payloadVars)
	if err != nil {
		return fmt.Errorf("failed to marshal variables: %w", err)
	}

	jobID := ids.NewEngineKey()
	if err := engineRepo.CreateJobByKeyTx(tx, jobID, exec.ProcessInstanceKey, exec.ID, *node.JobType, payload); err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	if err := engineRepo.UpdateExecutionStatusByKeyTx(tx, exec.ProcessInstanceKey, exec.ID, "waiting"); err != nil {
		return fmt.Errorf("failed to update execution status: %w", err)
	}

	graph, err := engineRepo.GetProcessGraphByInstanceID(exec.ProcessInstanceID)
	if err != nil {
		return fmt.Errorf("failed to load process graph: %w", err)
	}
	if err := r.armBoundaryEventsTx(tx, engineRepo, graph, exec, node); err != nil {
		return err
	}
	if err := repository.NotifyJobAvailableTx(tx, *node.JobType); err != nil {
		return fmt.Errorf("failed to queue job notification: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	committed = true
	r.auditService.LogJobCreated(exec.ProcessInstanceID, jobID, *node.JobType)

	if p := eventlog.Global(); p != nil {
		p.JobCreated(exec.ProcessInstanceID.String(), strconv.FormatInt(jobID, 10), strconv.FormatInt(exec.ID, 10), *node.JobType)
	}
	return nil
}
