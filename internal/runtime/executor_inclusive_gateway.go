package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/common"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/eventlog"
	"github.com/jeremielodi/bpmngo/internal/events"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jeremielodi/bpmngo/internal/service"
	"github.com/jmoiron/sqlx"
)

type InclusiveGatewayExecutor struct {
	db         *sqlx.DB
	engineRepo *repository.EngineRepository
}

func NewInclusiveGatewayExecutor(db *sqlx.DB, dispatcher *events.TaskEventDispatcher) *InclusiveGatewayExecutor {
	return &InclusiveGatewayExecutor{
		db:         db,
		engineRepo: repository.NewEngineRepository(db, dispatcher),
	}
}

// ExecuteFork evaluates conditions and creates child executions only for matching flows.
func (e *InclusiveGatewayExecutor) ExecuteFork(
	ctx context.Context,
	execID int64,
	gatewayNode *engine.Node,
	variables map[string]interface{},
) error {
	exec, err := e.engineRepo.GetActiveExecution(ctx, execID)
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if exec == nil {
		return fmt.Errorf("execution not found: %d", execID)
	}

	activeBranches, err := common.ResolveInclusiveBranches(gatewayNode, variables)
	if err != nil {
		return err
	}

	tx, err := e.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	branchVars, _ := json.Marshal(variables)

	for i, flow := range activeBranches {
		childExecID := ids.NewEngineKey()
		pathID := fmt.Sprintf("%d_%d", exec.ID, i)

		if err = e.engineRepo.CreateExecutionTx(tx, &repository.Execution{
			ID:                childExecID,
			ProcessInstanceID: exec.ProcessInstanceID,
			ParentExecutionID: &exec.ID,
			CurrentElementID:  flow.TargetRef,
			Status:            "active",
			IsActive:          true,
			PathID:            &pathID,
		}); err != nil {
			return fmt.Errorf("failed to create child execution: %w", err)
		}

		if _, err = tx.Exec(`
			INSERT INTO public.execution_variables (execution_key, variables, created_at)
			VALUES ($1, $2, NOW())
		`, childExecID, branchVars); err != nil {
			return fmt.Errorf("failed to store branch variables: %w", err)
		}
	}

	if err = e.engineRepo.UpdateExecutionStatusTx(tx, exec.ProcessInstanceID, exec.ID, "waiting"); err != nil {
		return fmt.Errorf("failed to mark parent as waiting: %w", err)
	}

	fmt.Printf("Inclusive fork at gateway %s: %d/%d branches active\n",
		gatewayNode.ID, len(activeBranches), len(gatewayNode.Outgoing))

	return tx.Commit()
}

// ExecuteJoin waits for all activated branches then resumes parent.
// Logic is identical to the parallel gateway join — expected_incoming is set from
// the actual number of child executions created at fork time.
func (e *InclusiveGatewayExecutor) ExecuteJoin(
	ctx context.Context,
	execID int64,
	gatewayNode *engine.Node,
) error {
	exec, err := e.engineRepo.GetActiveExecution(ctx, execID)
	if err != nil {
		return fmt.Errorf("failed to get execution: %w", err)
	}
	if exec == nil {
		return fmt.Errorf("execution not found: %d", execID)
	}
	if exec.ParentExecutionID == nil {
		return fmt.Errorf("join gateway called on execution with no parent")
	}

	tx, err := e.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var gatewayState models.GatewayState
	err = tx.Get(&gatewayState, `
		SELECT g.id, pi.id AS process_instance_id, g.process_instance_key, g.expected_incoming, g.received_incoming, g.status, g.joined_flows
		FROM public.gateway_state g
		JOIN public.process_instances pi ON pi.engine_key = g.process_instance_key
		WHERE g.process_instance_key = $1 AND g.gateway_id = $2 AND g.status = 'waiting'
		FOR UPDATE
	`, exec.ProcessInstanceKey, gatewayNode.ID)

	isFirstJoin := err != nil

	if isFirstJoin {
		childCount, err := e.getChildExecutionCount(tx, exec.ParentExecutionID)
		if err != nil {
			return err
		}

		joinedFlows := []string{*exec.PathID}
		joinedFlowsJSON, _ := json.Marshal(joinedFlows)

		gatewayState = models.GatewayState{
			ID:                 uuid.New(),
			ProcessInstanceID:  exec.ProcessInstanceID,
			ProcessInstanceKey: exec.ProcessInstanceKey,
			GatewayID:          gatewayNode.ID,
			ExpectedIncoming:   childCount,
			ReceivedIncoming:   1,
			Status:             "waiting",
			JoinedFlows:        joinedFlowsJSON,
		}

		created, err := e.engineRepo.CreateGatewayStateTx(tx, &gatewayState)
		if err != nil {
			return fmt.Errorf("failed to create gateway state: %w", err)
		}
		if !created {
			// Lost the race to a concurrent branch — re-fetch the row it
			// just created and fall through to the join-update path below.
			isFirstJoin = false
			if err := tx.Get(&gatewayState, `
				SELECT g.id, pi.id AS process_instance_id, g.process_instance_key, g.expected_incoming, g.received_incoming, g.status, g.joined_flows
				FROM public.gateway_state g
				JOIN public.process_instances pi ON pi.engine_key = g.process_instance_key
				WHERE g.process_instance_key = $1 AND g.gateway_id = $2 AND g.status = 'waiting'
				FOR UPDATE
			`, exec.ProcessInstanceKey, gatewayNode.ID); err != nil {
				return fmt.Errorf("failed to re-fetch gateway state after losing create race: %w", err)
			}
		}
	}
	if !isFirstJoin {
		var joinedFlows []string
		json.Unmarshal(gatewayState.JoinedFlows, &joinedFlows)
		if exec.PathID != nil {
			joinedFlows = append(joinedFlows, *exec.PathID)
		}
		joinedFlowsJSON, _ := json.Marshal(joinedFlows)
		newReceived := gatewayState.ReceivedIncoming + 1

		if _, err = tx.Exec(`
			UPDATE public.gateway_state
			SET received_incoming = $1, joined_flows = $2
			WHERE process_instance_key = $3 AND id = $4
		`, newReceived, joinedFlowsJSON, gatewayState.ProcessInstanceKey, gatewayState.ID); err != nil {
			return fmt.Errorf("failed to update gateway state: %w", err)
		}

		gatewayState.ReceivedIncoming = newReceived
	}

	if err = e.engineRepo.CompleteChildExecutionTx(tx, exec.ProcessInstanceID, exec.ID); err != nil {
		return fmt.Errorf("failed to complete child execution: %w", err)
	}

	joined := false
	var mergedVariables map[string]interface{}
	var nextNodeID string

	if gatewayState.ReceivedIncoming == gatewayState.ExpectedIncoming {
		joined = true
		mergedVariables, err = e.mergeBranchVariables(tx, exec.ParentExecutionID)
		if err != nil {
			return fmt.Errorf("failed to merge branch variables: %w", err)
		}
		if len(mergedVariables) > 0 {
			if err = e.engineRepo.MergeProcessVariablesTx(tx, exec.ProcessInstanceID, mergedVariables); err != nil {
				return fmt.Errorf("failed to merge variables: %w", err)
			}
		}

		nextNodeID = gatewayNode.Outgoing[0].TargetRef
		if err = e.engineRepo.UpdateExecutionStatusAndNodeTx(tx, exec.ProcessInstanceID, *exec.ParentExecutionID, "active", nextNodeID); err != nil {
			return fmt.Errorf("failed to resume parent execution: %w", err)
		}

		if _, err = tx.Exec(`
			UPDATE public.gateway_state SET status = 'completed', completed_at = NOW()
			WHERE process_instance_key = $1 AND id = $2
		`, gatewayState.ProcessInstanceKey, gatewayState.ID); err != nil {
			return fmt.Errorf("failed to complete gateway state: %w", err)
		}

		fmt.Printf("Inclusive join at gateway %s completed: all %d branches joined\n",
			gatewayNode.ID, gatewayState.ExpectedIncoming)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if joined {
		auditService := service.NewAuditService(e.db)
		if len(mergedVariables) > 0 {
			service.LogAuditErr("variables merged", auditService.LogVariablesMerged(exec.ProcessInstanceID, mergedVariables, "gatewayJoin"))
		}
		service.LogAuditErr("execution moved", auditService.LogExecutionMoved(*exec.ParentExecutionID, exec.ProcessInstanceID, gatewayNode.ID, nextNodeID, nil))
		if p := eventlog.Global(); p != nil {
			p.ExecutionMoved(exec.ProcessInstanceID.String(), strconv.FormatInt(*exec.ParentExecutionID, 10), gatewayNode.ID, nextNodeID)
		}
	}

	return nil
}

func (e *InclusiveGatewayExecutor) getChildExecutionCount(tx *sqlx.Tx, parentExecID *int64) (int, error) {
	if parentExecID == nil {
		return 0, fmt.Errorf("parent execution ID is nil")
	}
	var count int
	err := tx.Get(&count, `
		SELECT COUNT(*) FROM public.executions
		WHERE parent_execution_key = $1 AND is_active = true
	`, parentExecID)
	return count, err
}

func (e *InclusiveGatewayExecutor) mergeBranchVariables(
	tx *sqlx.Tx,
	parentExecID *int64,
) (map[string]interface{}, error) {
	if parentExecID == nil {
		return make(map[string]interface{}), nil
	}

	var childExecs []struct {
		ID        int64           `db:"engine_key"`
		Variables json.RawMessage `db:"variables"`
	}

	err := tx.Select(&childExecs, `
		SELECT e.engine_key, ev.variables
		FROM public.executions e
		JOIN public.execution_variables ev ON ev.execution_key = e.engine_key
		WHERE e.parent_execution_key = $1 AND e.is_active = false
	`, parentExecID)
	if err != nil {
		return nil, err
	}

	merged := make(map[string]interface{})
	for _, child := range childExecs {
		var vars map[string]interface{}
		if err := json.Unmarshal(child.Variables, &vars); err != nil {
			continue
		}
		for k, v := range vars {
			merged[k] = v
		}
	}
	return merged, nil
}
