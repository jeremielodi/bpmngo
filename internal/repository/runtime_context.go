package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
)

// RuntimeInnerInstance is the multi-instance iteration, when the execution
// being resumed is an active child of a multi-instance body.
type RuntimeInnerInstance struct {
	BodyID     int64
	LoopIndex  int
	ActivityID string
}

// RuntimeExecutionContext contains everything needed to enter the runtime
// loop. Keeping it together prevents every resume from issuing separate
// execution, path, variable-scope, parent-chain, graph-key and multi-instance
// lookups.
type RuntimeExecutionContext struct {
	Execution           *Execution
	ProcessDefinitionID uuid.UUID
	Variables           map[string]interface{}
	Inner               *RuntimeInnerInstance
}

// GetRuntimeExecutionContext loads an active execution and its visible
// variables in one database round trip. The correlated aggregates stay keyed
// by process_instance_key, which keeps the reads colocated on Citus.
func (r *EngineRepository) GetRuntimeExecutionContext(ctx context.Context, execID int64) (*RuntimeExecutionContext, error) {
	var row struct {
		ID                  int64        `db:"engine_key"`
		ProcessInstanceID   uuid.UUID    `db:"process_instance_id"`
		ProcessInstanceKey  int64        `db:"process_instance_key"`
		ProcessDefinitionID uuid.UUID    `db:"process_definition_id"`
		CurrentElementID    string       `db:"current_element_id"`
		Status              string       `db:"status"`
		IsActive            bool         `db:"is_active"`
		ParentExecutionID   *int64       `db:"parent_execution_key"`
		PathID              *string      `db:"path_id"`
		CreatedAt           sql.NullTime `db:"created_at"`
		UpdatedAt           sql.NullTime `db:"updated_at"`
		RootVariables       []byte       `db:"root_variables"`
		LocalScopes         []byte       `db:"local_scopes"`
		ExecutionParents    []byte       `db:"execution_parents"`
		InnerBodyID         *int64       `db:"inner_body_id"`
		InnerLoopIndex      *int         `db:"inner_loop_index"`
		InnerActivityID     *string      `db:"inner_activity_id"`
	}

	err := r.db.GetContext(ctx, &row, `
		SELECT e.engine_key,
		       pi.id AS process_instance_id,
		       e.process_instance_key,
		       pi.process_definition_id,
		       e.current_element_id,
		       e.status,
		       e.is_active,
		       e.parent_execution_key,
		       e.path_id,
		       e.created_at,
		       e.updated_at,
		       COALESCE(v.data, '{}'::jsonb) AS root_variables,
		       COALESCE((
		           SELECT jsonb_object_agg(s.scope_key::text, s.data)
		           FROM public.scope_variables s
		           WHERE s.process_instance_key = e.process_instance_key
		       ), '{}'::jsonb) AS local_scopes,
		       COALESCE((
		           SELECT jsonb_object_agg(x.engine_key::text, x.parent_execution_key)
		           FROM public.executions x
		           WHERE x.process_instance_key = e.process_instance_key
		       ), '{}'::jsonb) AS execution_parents,
		       mic.parent_execution_key AS inner_body_id,
		       mic.loop_index AS inner_loop_index,
		       mie.activity_id AS inner_activity_id
		FROM public.executions e
		JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		LEFT JOIN public.variables v ON v.process_instance_key = e.process_instance_key
		LEFT JOIN public.multi_instance_children mic
		       ON mic.child_execution_key = e.engine_key AND mic.status = 'active'
		LEFT JOIN public.multi_instance_executions mie
		       ON mie.process_instance_key = e.process_instance_key
		      AND mie.execution_key = mic.parent_execution_key
		      AND mie.status = 'active'
		WHERE e.engine_key = $1 AND e.is_active = true
	`, execID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	exec := &Execution{
		ID:                 row.ID,
		ProcessInstanceID:  row.ProcessInstanceID,
		ProcessInstanceKey: row.ProcessInstanceKey,
		CurrentElementID:   row.CurrentElementID,
		Status:             row.Status,
		ParentExecutionID:  row.ParentExecutionID,
		PathID:             row.PathID,
		IsActive:           row.IsActive,
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
	variables := map[string]interface{}{}
	if len(row.RootVariables) > 0 {
		if err := json.Unmarshal(row.RootVariables, &variables); err != nil {
			return nil, err
		}
	}
	localScopes := map[string]map[string]interface{}{}
	if len(row.LocalScopes) > 0 {
		if err := json.Unmarshal(row.LocalScopes, &localScopes); err != nil {
			return nil, err
		}
	}
	parents := map[string]*int64{}
	if len(row.ExecutionParents) > 0 {
		if err := json.Unmarshal(row.ExecutionParents, &parents); err != nil {
			return nil, err
		}
	}

	scopeID := row.ID
	if attachedTo, ok := ParseBoundaryTriggerPathID(row.PathID); ok {
		scopeID = attachedTo
	}
	chain := make([]int64, 0, 4)
	seen := map[int64]bool{}
	for id := &scopeID; id != nil && !seen[*id]; id = parents[strconv.FormatInt(*id, 10)] {
		seen[*id] = true
		chain = append(chain, *id)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for name, value := range localScopes[strconv.FormatInt(chain[i], 10)] {
			variables[name] = value
		}
	}

	result := &RuntimeExecutionContext{
		Execution:           exec,
		ProcessDefinitionID: row.ProcessDefinitionID,
		Variables:           variables,
	}
	if row.InnerBodyID != nil && row.InnerLoopIndex != nil && row.InnerActivityID != nil {
		result.Inner = &RuntimeInnerInstance{
			BodyID:     *row.InnerBodyID,
			LoopIndex:  *row.InnerLoopIndex,
			ActivityID: *row.InnerActivityID,
		}
	}
	RememberProcessInstanceKey(row.ProcessInstanceID, row.ProcessInstanceKey)
	return result, nil
}
