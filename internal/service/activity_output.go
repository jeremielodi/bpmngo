package service

import (
	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/common"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// ApplyActivityOutputTx sets the variables a completed activity hands back
// (a job's or a user task's), as in Camunda 8:
//
//   - without output mappings, each variable propagates from the
//     activity's scope (repository.SetVariablesTx);
//   - with output mappings, the variables become local to the activity and
//     only the mapping targets, evaluated in the activity's scope, are set
//     from the enclosing scope outwards.
//
// The activity's local scope (its input mappings) is dropped as it is left
// — except for an inner instance of a multi-instance activity, whose scope
// still serves its outputElement (see runtime.completeInnerInstance).
func ApplyActivityOutputTx(tx *sqlx.Tx, instanceID uuid.UUID, execID int64, node *engine.Node, vars map[string]interface{}) error {
	if len(node.OutputMappings) > 0 {
		if err := repository.SetLocalVariablesTx(tx, instanceID, execID, vars); err != nil {
			return err
		}
		view, err := repository.VariablesForExecution(tx, instanceID, execID)
		if err != nil {
			return err
		}
		mapped := common.EvaluateIOMappings(node.OutputMappings, view)
		var parent *int64
		if err := tx.Get(&parent, `
			SELECT parent_execution_key FROM public.executions
			WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
			  AND engine_key = $2
		`, instanceID, execID); err != nil {
			return err
		}
		scope := int64(0)
		if parent != nil {
			scope = *parent
		}
		if err := repository.SetVariablesTx(tx, instanceID, scope, mapped); err != nil {
			return err
		}
	} else if err := repository.SetVariablesTx(tx, instanceID, execID, vars); err != nil {
		return err
	}
	if node.MultiInstance == nil {
		return repository.ClearLocalVariablesTx(tx, instanceID, execID)
	}
	return nil
}
