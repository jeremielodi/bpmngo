package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jmoiron/sqlx"
)

// Variable scopes, as in Camunda 8. An instance's variables live in nested
// scopes: the instance itself (the variables table) and local scopes held
// by executions (scope_variables, keyed by the execution's id) — an
// embedded sub-process, a multi-instance body and each of its iterations,
// an activity with input mappings.
//
//   - Reading from an execution sees its own scope and every enclosing one
//     (walking parent_execution_id), the innermost value winning.
//   - Setting variables from an execution propagates each one outwards: it
//     updates the nearest scope that already has it, or else the instance
//     scope.
//   - Setting local variables puts them in one scope only.
//
// The tree is walked in Go from one per-instance query — no recursive SQL
// (Citus-safe; see architecture/citus-distributed-storage-design.md).

// scopeChain returns execID and its ancestors, innermost first.
func scopeChain(q sqlx.Queryer, instanceID uuid.UUID, execID int64) ([]int64, error) {
	if execID == 0 {
		return nil, nil
	}
	instanceKey, err := processInstanceEngineKey(q, instanceID)
	if err != nil {
		return nil, err
	}
	return scopeChainByKey(q, instanceKey, execID)
}

func scopeChainByKey(q sqlx.Queryer, instanceKey, execID int64) ([]int64, error) {
	if execID == 0 {
		return nil, nil
	}
	rows, err := q.Queryx(`
		SELECT engine_key, parent_execution_key FROM public.executions
		WHERE process_instance_key = $1
	`, instanceKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parent := map[int64]*int64{}
	for rows.Next() {
		var id int64
		var p *int64
		if err := rows.Scan(&id, &p); err != nil {
			return nil, err
		}
		parent[id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var chain []int64
	seen := map[int64]bool{}
	for id := &execID; id != nil && !seen[*id]; id = parent[*id] {
		seen[*id] = true
		chain = append(chain, *id)
	}
	return chain, nil
}

// VariableScopeOf returns the execution whose scopes execID reads and
// writes variables through: itself, except a boundary event's trigger
// execution (which has no parent), which goes through the activity it is
// attached to.
func VariableScopeOf(q sqlx.Queryer, instanceID uuid.UUID, execID int64) int64 {
	instanceKey, err := processInstanceEngineKey(q, instanceID)
	if err != nil {
		return execID
	}
	return VariableScopeOfByKey(q, instanceKey, execID)
}

func VariableScopeOfByKey(q sqlx.Queryer, instanceKey, execID int64) int64 {
	var pathID *string
	if err := sqlx.Get(q, &pathID, `
		SELECT path_id FROM public.executions
		WHERE process_instance_key = $1
		  AND engine_key = $2
	`, instanceKey, execID); err != nil {
		return execID
	}
	if activity, ok := ParseBoundaryTriggerPathID(pathID); ok {
		return activity
	}
	return execID
}

// localScopes loads an instance's local scopes, by scope id.
func localScopes(q sqlx.Queryer, instanceID uuid.UUID, forUpdate bool) (map[int64]map[string]interface{}, error) {
	instanceKey, err := processInstanceEngineKey(q, instanceID)
	if err != nil {
		return nil, err
	}
	return localScopesByKey(q, instanceKey, forUpdate)
}

func localScopesByKey(q sqlx.Queryer, instanceKey int64, forUpdate bool) (map[int64]map[string]interface{}, error) {
	query := `SELECT scope_key, data FROM public.scope_variables WHERE process_instance_key = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	rows, err := q.Queryx(query, instanceKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := map[int64]map[string]interface{}{}
	for rows.Next() {
		var id int64
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		vars := map[string]interface{}{}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &vars); err != nil {
				return nil, err
			}
		}
		scopes[id] = vars
	}
	return scopes, rows.Err()
}

func rootVariables(q sqlx.Queryer, instanceID uuid.UUID, forUpdate bool) (map[string]interface{}, error) {
	instanceKey, err := processInstanceEngineKey(q, instanceID)
	if err != nil {
		return nil, err
	}
	return rootVariablesByKey(q, instanceKey, forUpdate)
}

func rootVariablesByKey(q sqlx.Queryer, instanceKey int64, forUpdate bool) (map[string]interface{}, error) {
	query := `SELECT data FROM public.variables WHERE process_instance_key = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var data []byte
	if err := sqlx.Get(q, &data, query, instanceKey); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	vars := map[string]interface{}{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &vars); err != nil {
			return nil, err
		}
	}
	return vars, nil
}

// VariablesForExecution returns the variables execID sees: the instance's,
// overlaid by each enclosing local scope from the outermost in. uuid.Nil
// returns the instance scope only.
func VariablesForExecution(q sqlx.Queryer, instanceID uuid.UUID, execID int64) (map[string]interface{}, error) {
	instanceKey, err := processInstanceEngineKey(q, instanceID)
	if err != nil {
		return nil, err
	}
	return VariablesForExecutionByKey(q, instanceKey, execID)
}

// VariablesForExecutionByKey avoids three UUID-to-BIGINT lookups on every
// runtime resume. The execution row that initiated the resume is the source
// of truth for instanceKey.
func VariablesForExecutionByKey(q sqlx.Queryer, instanceKey, execID int64) (map[string]interface{}, error) {
	vars, err := rootVariablesByKey(q, instanceKey, false)
	if err != nil {
		return nil, err
	}
	scopes, err := localScopesByKey(q, instanceKey, false)
	if err != nil || len(scopes) == 0 {
		return vars, err
	}
	chain, err := scopeChainByKey(q, instanceKey, execID)
	if err != nil {
		return nil, err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range scopes[chain[i]] {
			vars[k] = v
		}
	}
	return vars, nil
}

// LocalVariables returns the variables local to one scope.
func LocalVariables(q sqlx.Queryer, instanceID uuid.UUID, scopeID int64) (map[string]interface{}, error) {
	var data []byte
	err := sqlx.Get(q, &data, `
		SELECT data FROM public.scope_variables
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND scope_key = $2
	`, instanceID, scopeID)
	vars := map[string]interface{}{}
	if err == sql.ErrNoRows {
		return vars, nil
	}
	if err != nil {
		return nil, err
	}
	return vars, json.Unmarshal(data, &vars)
}

// SetLocalVariablesTx merges vars into scopeID's local scope.
func SetLocalVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, scopeID int64, vars map[string]interface{}) error {
	if len(vars) == 0 {
		return nil
	}
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	return SetLocalVariablesByKeyTx(tx, instanceID, instanceKey, scopeID, vars)
}

func SetLocalVariablesByKeyTx(tx *sqlx.Tx, instanceID uuid.UUID, instanceKey, scopeID int64, vars map[string]interface{}) error {
	if len(vars) == 0 {
		return nil
	}
	data, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO public.scope_variables (process_instance_key, scope_key, data, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (process_instance_key, scope_key)
		DO UPDATE SET data = public.scope_variables.data || EXCLUDED.data, updated_at = EXCLUDED.updated_at
	`, instanceKey, scopeID, data, time.Now())
	return notifyVariables(tx, instanceID, err)
}

// SetVariablesTx sets vars from execID's scope outwards: each one updates
// the nearest scope that already has it, or the instance scope. uuid.Nil
// sets them all on the instance scope.
func SetVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, execID int64, vars map[string]interface{}) error {
	if len(vars) == 0 {
		return nil
	}
	root, err := rootVariables(tx, instanceID, true)
	if err != nil {
		return err
	}
	scopes, err := localScopes(tx, instanceID, true)
	if err != nil {
		return err
	}
	var chain []int64
	if len(scopes) > 0 {
		if chain, err = scopeChain(tx, instanceID, execID); err != nil {
			return err
		}
	}
	touched := map[int64]map[string]interface{}{}
	rootChanged := false
	for name, value := range vars {
		placed := false
		for _, scopeID := range chain {
			if local, ok := scopes[scopeID]; ok {
				if _, has := local[name]; has {
					if touched[scopeID] == nil {
						touched[scopeID] = map[string]interface{}{}
					}
					touched[scopeID][name] = value
					placed = true
					break
				}
			}
		}
		if !placed {
			root[name] = value
			rootChanged = true
		}
	}
	for scopeID, changed := range touched {
		if err := SetLocalVariablesTx(tx, instanceID, scopeID, changed); err != nil {
			return err
		}
	}
	if rootChanged {
		data, err := json.Marshal(root)
		if err != nil {
			return err
		}
		instanceKey, err := processInstanceEngineKey(tx, instanceID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT INTO public.variables (engine_key, process_instance_key, data, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (process_instance_key) DO UPDATE SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at
		`, ids.NewEngineKey(), instanceKey, data, time.Now()); err != nil {
			return fmt.Errorf("instance variables: %w", err)
		}
		return notifyVariables(tx, instanceID, nil)
	}
	return nil
}

// ClearLocalVariablesTx drops scopes when the executions holding them
// leave their element.
func ClearLocalVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, scopeIDs ...int64) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	for _, id := range scopeIDs {
		if _, err := tx.Exec(`
			DELETE FROM public.scope_variables
			WHERE process_instance_key = $1
			  AND scope_key = $2
		`, instanceKey, id); err != nil {
			return err
		}
	}
	return nil
}
