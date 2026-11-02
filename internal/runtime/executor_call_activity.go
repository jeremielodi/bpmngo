package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jeremielodi/bpmngo/internal/common"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/feel"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
)

// executeCallActivity starts a child process instance and parks the parent execution.
func (r *Runtime) executeCallActivity(ctx context.Context, engineRepo *repository.EngineRepository, exec *repository.Execution, node *engine.Node, variables map[string]interface{}) error {
	calledID := strings.TrimSpace(node.CalledElement)
	if strings.HasPrefix(calledID, "=") {
		// zeebe:calledElement processId given as a FEEL expression.
		v, err := feel.Evaluate(trimFEEL(calledID), variables)
		if err != nil {
			return fmt.Errorf("callActivity %s: processId %q: %w", node.ID, calledID, err)
		}
		s, ok := v.(string)
		if !ok || s == "" {
			return models.WithIncidentType(models.IncidentCalledElementError, fmt.Errorf("callActivity %s: processId %q is %v, not a process id", node.ID, calledID, v))
		}
		calledID = s
	}
	if calledID == "" {
		return models.WithIncidentType(models.IncidentCalledElementError, fmt.Errorf("callActivity %s has no calledElement", node.ID))
	}

	// The called process runs in the caller's tenant: resolved there, and
	// stamped with it.
	var parentTenant *string
	if err := r.DB.Get(&parentTenant, `SELECT tenant_id FROM process_instances WHERE id = $1`, exec.ProcessInstanceID); err != nil {
		return fmt.Errorf("callActivity: read caller tenant: %w", err)
	}
	tenant := ""
	if parentTenant != nil {
		tenant = *parentTenant
	}

	// Find the called process definition
	procDef, err := engineRepo.FindLatestProcessDefinitionByKeyForTenant(calledID, tenant)
	if err != nil {
		return models.WithIncidentType(models.IncidentCalledElementError, fmt.Errorf("callActivity: cannot find process '%s': %w", calledID, err))
	}

	// Build the child process graph
	childGraph, err := engine.BuildGraphForProcess([]byte(procDef.BpmnXML), calledID)
	if err != nil {
		return fmt.Errorf("callActivity: build child graph: %w", err)
	}

	// Find start node in child graph
	var startNodeID string
	for _, n := range childGraph.Nodes {
		if n.IsProcessStart() || n.Type == engine.MessageStartEventType {
			startNodeID = n.ID
			break
		}
	}
	if startNodeID == "" {
		return models.WithIncidentType(models.IncidentCalledElementError, fmt.Errorf("callActivity: no start event in called process '%s'", node.CalledElement))
	}

	now := time.Now()
	childInstanceID := ids.NewGlobalID()
	childExecID := ids.NewEngineKey()

	tx, err := r.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("callActivity: begin tx: %w", err)
	}
	defer tx.Rollback()
	definitionKey, err := repository.ProcessDefinitionEngineKey(tx, procDef.ID)
	if err != nil {
		return fmt.Errorf("callActivity: resolve child definition: %w", err)
	}
	parentInstanceKey, err := repository.ProcessInstanceEngineKey(tx, exec.ProcessInstanceID)
	if err != nil {
		return fmt.Errorf("callActivity: resolve parent instance: %w", err)
	}
	childInstanceKey := ids.NewEngineKey()

	// Create child process instance with parent tracking
	_, err = tx.Exec(`
		INSERT INTO process_instances
		  (id, engine_key, process_definition_id, process_definition_key, status, started_at,
		   parent_instance_id, parent_process_instance_key, parent_execution_key, tenant_id)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, $8, $9)
	`, childInstanceID, childInstanceKey, procDef.ID, definitionKey, now, exec.ProcessInstanceID, parentInstanceKey, exec.ID, parentTenant)
	if err != nil {
		return fmt.Errorf("callActivity: create child instance: %w", err)
	}

	// The child's variables: every variable the call activity sees (unless
	// propagateAllParentVariables is off), plus its input mappings — which
	// are also the call activity's local variables, as in Camunda 8.
	childVars := map[string]interface{}{}
	if !node.NoParentVariables {
		for k, v := range variables {
			childVars[k] = v
		}
	}
	if inputs := common.EvaluateIOMappings(node.InputMappings, variables); len(inputs) > 0 {
		for k, v := range inputs {
			childVars[k] = v
		}
		if err := repository.SetLocalVariablesTx(tx, exec.ProcessInstanceID, exec.ID, inputs); err != nil {
			return models.WithIncidentType(models.IncidentIOMappingError, fmt.Errorf("callActivity: input mappings: %w", err))
		}
	}
	if err := engineRepo.CreateVariablesTx(tx, childInstanceID, childVars, now); err != nil {
		return fmt.Errorf("callActivity: copy variables: %w", err)
	}

	// Create child execution at start node
	_, err = tx.Exec(`
		INSERT INTO executions
		  (engine_key, process_instance_key, current_element_id, status, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', true, $4, $4)
	`, childExecID, childInstanceKey, startNodeID, now)
	if err != nil {
		return fmt.Errorf("callActivity: create child execution: %w", err)
	}

	// Park parent execution (waiting for child to complete)
	_, err = tx.Exec(`
		UPDATE executions SET status = 'waiting', updated_at = NOW()
		WHERE process_instance_key = $1 AND engine_key = $2
	`, parentInstanceKey, exec.ID)
	if err != nil {
		return fmt.Errorf("callActivity: park parent: %w", err)
	}

	parentGraph, err := engineRepo.GetProcessGraphByInstanceID(exec.ProcessInstanceID)
	if err != nil {
		return fmt.Errorf("callActivity: load process graph: %w", err)
	}
	if err := r.armBoundaryEventsTx(tx, engineRepo, parentGraph, exec, node); err != nil {
		return fmt.Errorf("callActivity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("callActivity: commit: %w", err)
	}

	// Execute the child process (synchronously within this goroutine)
	childRT := NewRuntime(childGraph, r.DB, r.dispatcher)
	return childRT.ExecuteExecution(ctx, childExecID)
}
