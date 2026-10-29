package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

type ProcessInstanceRepository struct {
	db *sqlx.DB
}

func NewProcessInstanceRepository(db *sqlx.DB) *ProcessInstanceRepository {
	return &ProcessInstanceRepository{db: db}
}

// CreateProcessInstance creates a new process instance. tenantID is
// stamped from the process definition being instantiated (nil = no
// tenant / untenanted deployment) so list/search/get endpoints can filter
// by the caller's tenant without a join.
func (r *ProcessInstanceRepository) CreateProcessInstanceTx(tx *sqlx.Tx, instanceID, processDefID uuid.UUID, startedAt time.Time, tenantID *string) error {
	return r.CreateProcessInstanceByKeyTx(tx, instanceID, ids.NewEngineKey(), processDefID, startedAt, tenantID)
}

func (r *ProcessInstanceRepository) CreateProcessInstanceByKeyTx(tx *sqlx.Tx, instanceID uuid.UUID, instanceKey int64, processDefID uuid.UUID, startedAt time.Time, tenantID *string) error {
	definitionKey, err := processDefinitionEngineKey(tx, processDefID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO public.process_instances
			(id, engine_key, process_definition_id, process_definition_key, status, started_at, tenant_id)
		VALUES ($1, $2, $3, $4, 'running', $5, $6)
	`, instanceID, instanceKey, processDefID, definitionKey, startedAt, tenantID)
	return err
}

// CreateVariables creates initial variables for a process instance
func (r *ProcessInstanceRepository) CreateVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, variables map[string]interface{}, updatedAt time.Time) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	return r.CreateVariablesByKeyTx(tx, instanceID, instanceKey, variables, updatedAt)
}

func (r *ProcessInstanceRepository) CreateVariablesByKeyTx(tx *sqlx.Tx, instanceID uuid.UUID, instanceKey int64, variables map[string]interface{}, updatedAt time.Time) error {
	varData, err := json.Marshal(variables)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO public.variables
			(engine_key, process_instance_key, data, updated_at)
		VALUES ($1, $2, $3, $4)
	`, ids.NewEngineKey(), instanceKey, varData, updatedAt)
	return notifyVariables(tx, instanceID, err)
}

// CreateExecution creates an initial execution record
func (r *ProcessInstanceRepository) CreateExecutionTx(tx *sqlx.Tx, execID int64, instanceID uuid.UUID, startNodeID string, now time.Time) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	return r.CreateExecutionByKeyTx(tx, execID, instanceKey, startNodeID, now)
}

func (r *ProcessInstanceRepository) CreateExecutionByKeyTx(tx *sqlx.Tx, execID, instanceKey int64, startNodeID string, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO public.executions
			(engine_key, process_instance_key, current_element_id, status, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', true, $4, $5)
	`, execID, instanceKey, startNodeID, now, now)
	return err
}

// GetVariables returns the current variables for a process instance
func (r *ProcessInstanceRepository) GetVariables(instanceID uuid.UUID) (map[string]interface{}, error) {
	var data []byte
	err := r.db.Get(&data, `SELECT data FROM public.variables WHERE process_instance_key = (SELECT engine_key FROM process_instances WHERE id = $1)`, instanceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	vars := make(map[string]interface{})
	if len(data) > 0 {
		if err := json.Unmarshal(data, &vars); err != nil {
			return nil, err
		}
	}
	return vars, nil
}

// UpsertVariables inserts or updates variables for a process instance
func (r *ProcessInstanceRepository) UpsertVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, vars map[string]interface{}) error {
	jsonData, err := json.Marshal(vars)
	if err != nil {
		return err
	}
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO public.variables (engine_key, process_instance_key, data, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (process_instance_key) DO UPDATE
		SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at
	`, ids.NewEngineKey(), instanceKey, jsonData, time.Now())
	return notifyVariables(tx, instanceID, err)
}

// GetProcessDefinitionID returns the definition_id of a process instance
func (r *ProcessInstanceRepository) GetProcessDefinitionID(instanceID uuid.UUID) (uuid.UUID, error) {
	var defID uuid.UUID
	err := r.db.Get(&defID, `SELECT process_definition_id FROM public.process_instances WHERE id = $1`, instanceID)
	return defID, err
}

// UpdateProcessInstanceStatus updates the status of a process instance
func (r *ProcessInstanceRepository) UpdateProcessInstanceStatusTx(tx *sqlx.Tx, instanceID uuid.UUID, status string, endTime *time.Time) error {
	query := `UPDATE public.process_instances SET status = $1`
	args := []interface{}{status}

	if endTime != nil {
		query += `, ended_at = $2`
		args = append(args, *endTime)
		query += ` WHERE id = $3`
		args = append(args, instanceID)
	} else {
		query += ` WHERE id = $2`
		args = append(args, instanceID)
	}

	_, err := tx.Exec(query, args...)
	return err
}

// internal/repository/process_instance_repository.go
// Add these methods

// UpsertVariables creates or updates variables for a process instance
func (r *ProcessInstanceRepository) UpsertVariables(instanceID uuid.UUID, variables map[string]interface{}) error {
	jsonData, err := json.Marshal(variables)
	if err != nil {
		return err
	}
	instanceKey, err := processInstanceEngineKey(r.db, instanceID)
	if err != nil {
		return err
	}

	// Check if variables exist
	var exists bool
	err = r.db.Get(&exists, `
		SELECT EXISTS(SELECT 1 FROM public.variables WHERE process_instance_key = $1)
	`, instanceKey)
	if err != nil {
		return err
	}

	if exists {
		_, err = r.db.Exec(`
			UPDATE public.variables 
			SET data = $1, updated_at = NOW()
			WHERE process_instance_key = $2
		`, jsonData, instanceKey)
	} else {
		_, err = r.db.Exec(`
			INSERT INTO public.variables (engine_key, process_instance_key, data, updated_at)
			VALUES ($1, $2, $3, NOW())
		`, ids.NewEngineKey(), instanceKey, jsonData)
	}
	return notifyVariables(r.db, instanceID, err)
}

// FindAll retrieves process instances with optional filters. An empty
// tenantID means "no tenant filter" (superuser / untenanted deployment).
func (r *ProcessInstanceRepository) FindAll(status, processKey, tenantID string, businessKey ...string) ([]models.ProcessInstance, error) {
	bk := ""
	if len(businessKey) > 0 {
		bk = businessKey[0]
	}
	return r.FindAllLimit(status, processKey, tenantID, bk, 0)
}

// FindAllLimit is FindAll keeping only the limit newest instances (0: all).
func (r *ProcessInstanceRepository) FindAllLimit(status, processKey, tenantID, businessKey string, limit int) ([]models.ProcessInstance, error) {
	var instances []models.ProcessInstance
	query := `
		SELECT pi.id, pi.process_definition_id, pi.status, pi.started_by, pi.started_at, pi.ended_at, pi.tenant_id, pi.business_key,
		       pd.process_key, pd.process_name, pd.version
		FROM public.process_instances pi
		LEFT JOIN public.process_definitions pd ON pd.id = pi.process_definition_id
		WHERE 1=1
	`
	var args []interface{}
	argIndex := 1

	if status != "" {
		query += fmt.Sprintf(" AND pi.status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}
	if processKey != "" {
		query += fmt.Sprintf(" AND pd.process_key = $%d", argIndex)
		args = append(args, processKey)
		argIndex++
	}
	if tenantID != "" {
		query += fmt.Sprintf(" AND pi.tenant_id = $%d", argIndex)
		args = append(args, tenantID)
		argIndex++
	}

	if businessKey != "" {
		query += fmt.Sprintf(" AND pi.business_key = $%d", argIndex)
		args = append(args, businessKey)
		argIndex++
	}

	query += " ORDER BY pi.started_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, limit)
	}

	err := r.db.Select(&instances, query, args...)
	return instances, err
}

// internal/repository/process_instance_repository.go
// Add this method to the ProcessInstanceRepository struct

// FindByID retrieves a process instance by its ID
func (r *ProcessInstanceRepository) FindByID(id uuid.UUID) (*models.ProcessInstance, error) {
	var instance models.ProcessInstance

	query := `
		SELECT 
			pi.id,
			pi.process_definition_id,
			pi.status,
			pi.started_by,
			pi.transaction_scope_id,
			pi.started_at,
			pi.ended_at,
			pi.tenant_id,
			pi.business_key,
			pd.process_key,
			pd.process_name,
			pd.version
		FROM public.process_instances pi
		LEFT JOIN public.process_definitions pd ON pd.id = pi.process_definition_id
		WHERE pi.id = $1
	`

	err := r.db.Get(&instance, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &instance, nil
}

// internal/repository/process_instance_repository.go
// Add these missing methods to the ProcessInstanceRepository struct

// ============================================================
// PROCESS INSTANCE STATE MANAGEMENT
// ============================================================

// UpdateStatus updates the status of a process instance
func (r *ProcessInstanceRepository) UpdateStatus(instanceID uuid.UUID, status string) error {
	_, err := r.db.Exec(`
		UPDATE public.process_instances 
		SET status = $1, ended_at = NOW()
		WHERE id = $2
	`, status, instanceID)
	return err
}

// UpdateStatusTx updates the status of a process instance within a transaction
func (r *ProcessInstanceRepository) UpdateStatusTx(tx *sqlx.Tx, instanceID uuid.UUID, status string, endedAt *time.Time) error {
	query := `UPDATE public.process_instances SET status = $1`
	args := []interface{}{status}

	if endedAt != nil {
		query += `, ended_at = $2`
		args = append(args, *endedAt)
		query += ` WHERE id = $3`
		args = append(args, instanceID)
	} else {
		query += ` WHERE id = $2`
		args = append(args, instanceID)
	}

	_, err := tx.Exec(query, args...)
	return err
}

// ============================================================
// EXECUTION MANAGEMENT
// ============================================================

// GetActiveExecutions retrieves all active executions for a process instance
func (r *ProcessInstanceRepository) GetActiveExecutions(instanceID uuid.UUID) ([]Execution, error) {
	var executions []Execution
	err := r.db.Select(&executions, `
		SELECT e.engine_key, pi.id AS process_instance_id, e.process_instance_key, e.current_element_id, e.status, e.is_active,
		       e.parent_execution_key, e.path_id, e.created_at, e.updated_at
		FROM public.executions e JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		WHERE pi.id = $1 AND e.is_active = true
	`, instanceID)
	return executions, err
}

// FindExecutionsByInstance retrieves every execution (active or completed)
// for a process instance — completed executions are never deleted, only
// marked is_active=false, so this doubles as the flow-node instance history
// for the Operate-equivalent GET /v2/flownode-instances/search.
func (r *ProcessInstanceRepository) FindExecutionsByInstance(instanceID uuid.UUID) ([]Execution, error) {
	var executions []Execution
	err := r.db.Select(&executions, `
		SELECT e.engine_key, pi.id AS process_instance_id, e.process_instance_key, e.current_element_id, e.status, e.is_active,
		       e.parent_execution_key, e.path_id, e.created_at, e.updated_at
		FROM public.executions e JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		WHERE pi.id = $1
		ORDER BY e.created_at ASC
	`, instanceID)
	return executions, err
}

// FindRecentExecutions returns the limit most recently created executions
// of every live instance, newest first.
func (r *ProcessInstanceRepository) FindRecentExecutions(limit int) ([]Execution, error) {
	var executions []Execution
	err := r.db.Select(&executions, `
		SELECT e.engine_key, pi.id AS process_instance_id, e.process_instance_key, e.current_element_id, e.status, e.is_active,
		       e.parent_execution_key, e.path_id, e.created_at, e.updated_at
		FROM public.executions e JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		ORDER BY e.created_at DESC
		LIMIT $1
	`, limit)
	return executions, err
}

// TerminateActiveExecutionsByProcessInstanceTx marks every still-active or
// waiting execution (token) of a process instance as terminated — used when
// the whole instance is terminated, so no token is left looking "active" in
// live queries (e.g. v2 flownode-instance search, which keys off is_active)
// for a process that no longer runs.
func (r *ProcessInstanceRepository) TerminateActiveExecutionsByProcessInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		UPDATE public.executions
		SET status = 'terminated', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1
		  AND status IN ('active', 'waiting')
	`, instanceKey)
	return err
}

// ============================================================
// HISTORIC QUERIES
// ============================================================

// FindHistoricByID retrieves a completed/terminated process instance
// FindHistoricByID looks up a finished process instance. Finished instances
// no longer live in process_instances — they're archived into
// historic_process_instances and deleted from the live tables (see
// internal/service/archive_service.go) — so this reads the historic table,
// not a status-filtered live query.
func (r *ProcessInstanceRepository) FindHistoricByID(instanceID uuid.UUID) (*models.ProcessInstance, error) {
	h, err := NewHistoricArchiveRepository(r.db).FindProcessInstanceByID(instanceID)
	if err != nil || h == nil {
		return nil, err
	}
	instance := &models.ProcessInstance{
		ID:                  h.ID,
		ProcessDefinitionID: h.ProcessDefinitionID,
		Status:              h.State,
		StartedBy:           h.StartedBy,
		StartedAt:           h.StartedAt,
		EndedAt:             &h.EndedAt,
		ProcessKey:          h.ProcessDefinitionKey,
		TenantID:            h.TenantID,
	}
	if h.ProcessDefinitionVersion != nil {
		instance.Version = *h.ProcessDefinitionVersion
	}
	return instance, nil
}

// FindAllHistoric retrieves all completed/terminated process instances with filters
func (r *ProcessInstanceRepository) FindAllHistoric(status, processKey string, limit, offset int) ([]models.ProcessInstance, error) {
	var instances []models.ProcessInstance

	query := `
		SELECT 
			pi.id,
			pi.process_definition_id,
			pi.status,
			pi.started_by,
			pi.started_at,
			pi.ended_at,
			pd.process_key,
			pd.process_name,
			pd.version
		FROM public.process_instances pi
		LEFT JOIN public.process_definitions pd ON pd.id = pi.process_definition_id
		WHERE (pi.status = 'completed' OR pi.status = 'terminated')
	`

	var args []interface{}
	argIndex := 1

	if status != "" {
		query += fmt.Sprintf(" AND pi.status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}
	if processKey != "" {
		query += fmt.Sprintf(" AND pd.process_key = $%d", argIndex)
		args = append(args, processKey)
		argIndex++
	}

	query += " ORDER BY pi.started_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, limit)
		argIndex++
	}
	if offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, offset)
	}

	err := r.db.Select(&instances, query, args...)
	return instances, err
}

// CountHistoric counts completed/terminated process instances with filters
func (r *ProcessInstanceRepository) CountHistoric(status, processKey string) (int, error) {
	var count int

	query := `
		SELECT COUNT(*)
		FROM public.process_instances pi
		LEFT JOIN public.process_definitions pd ON pd.id = pi.process_definition_id
		WHERE (pi.status = 'completed' OR pi.status = 'terminated')
	`

	var args []interface{}
	argIndex := 1

	if status != "" {
		query += fmt.Sprintf(" AND pi.status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}
	if processKey != "" {
		query += fmt.Sprintf(" AND pd.process_key = $%d", argIndex)
		args = append(args, processKey)
		argIndex++
	}

	err := r.db.Get(&count, query, args...)
	return count, err
}

// ============================================================
// VARIABLE MANAGEMENT
// ============================================================

// DeleteVariable removes a single variable from a process instance
func (r *ProcessInstanceRepository) DeleteVariable(instanceID uuid.UUID, varName string) error {
	// Get current variables
	variables, err := r.GetVariables(instanceID)
	if err != nil {
		return err
	}

	// Delete variable
	delete(variables, varName)

	// Save updated variables
	return r.UpsertVariables(instanceID, variables)
}

// DeleteVariableTx removes a single variable from a process instance within a transaction
func (r *ProcessInstanceRepository) DeleteVariableTx(tx *sqlx.Tx, instanceID uuid.UUID, varName string) error {
	// Get current variables
	variables, err := r.GetVariables(instanceID)
	if err != nil {
		return err
	}

	// Delete variable
	delete(variables, varName)

	// Save updated variables
	return r.UpsertVariablesTx(tx, instanceID, variables)
}

// ============================================================
// PROCESS INSTANCE VALIDATION
// ============================================================

// Exists checks if a process instance exists
func (r *ProcessInstanceRepository) Exists(instanceID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.Get(&exists, `
		SELECT EXISTS(SELECT 1 FROM public.process_instances WHERE id = $1)
	`, instanceID)
	return exists, err
}

// IsActive checks if a process instance is still active (not completed/terminated)
func (r *ProcessInstanceRepository) IsActive(instanceID uuid.UUID) (bool, error) {
	var status string
	err := r.db.Get(&status, `
		SELECT status FROM public.process_instances WHERE id = $1
	`, instanceID)
	if err != nil {
		return false, err
	}
	return status == "running" || status == "suspended", nil
}

// ============================================================
// AUDIT LOGS
// ============================================================

// CreateAuditLog creates an audit log entry
func (r *ProcessInstanceRepository) CreateAuditLog(log models.AuditLog) error {
	var instanceKey *int64
	if log.ProcessInstanceID != nil {
		key, err := processInstanceEngineKey(r.db, *log.ProcessInstanceID)
		if err != nil {
			return err
		}
		instanceKey = &key
	}
	_, err := r.db.Exec(`
		INSERT INTO public.audit_logs 
		(id, process_instance_key, task_key, user_id, action, old_data, new_data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, uuid.New(), instanceKey, log.TaskID, log.UserID, log.Action, log.OldData, log.NewData, time.Now())
	return err
}

// GetAuditLogs retrieves audit logs for a process instance
func (r *ProcessInstanceRepository) GetAuditLogs(instanceID uuid.UUID) ([]models.AuditLog, error) {
	FlushAuditLogs() // rows still queued for their batch
	var logs []models.AuditLog
	err := r.db.Select(&logs, `
		SELECT a.id, pi.id AS process_instance_id, a.task_key, a.user_id, a.action, a.old_data, a.new_data, a.created_at
		FROM public.audit_logs a JOIN public.process_instances pi ON pi.engine_key = a.process_instance_key
		WHERE pi.id = $1
		ORDER BY a.created_at ASC
	`, instanceID)
	return logs, err
}

// ============================================================
// TASK MANAGEMENT
// ============================================================

// GetTasksByProcessInstance retrieves all tasks for a process instance
func (r *ProcessInstanceRepository) GetTasksByProcessInstance(instanceID uuid.UUID) ([]models.Task, error) {
	var tasks []models.Task
	err := r.db.Select(&tasks, `
		SELECT t.engine_key, pi.id AS process_instance_id, t.execution_key, t.task_definition_key, t.task_name,
		       t.assignee, t.candidate_group, t.status, t.form_data, t.created_at, t.claimed_at, t.completed_at
		FROM public.tasks t JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE pi.id = $1
		ORDER BY t.created_at ASC
	`, instanceID)
	return tasks, err
}

// ============================================================
// STATISTICS
// ============================================================

// GetStatistics returns statistics for process instances
func (r *ProcessInstanceRepository) GetStatistics() (map[string]int, error) {
	stats := make(map[string]int)

	var total int
	err := r.db.Get(&total, "SELECT COUNT(*) FROM public.process_instances")
	if err != nil {
		return nil, err
	}
	stats["total"] = total

	var running int
	err = r.db.Get(&running, "SELECT COUNT(*) FROM public.process_instances WHERE status = 'running'")
	if err != nil {
		return nil, err
	}
	stats["running"] = running

	var completed int
	err = r.db.Get(&completed, "SELECT COUNT(*) FROM public.process_instances WHERE status = 'completed'")
	if err != nil {
		return nil, err
	}
	stats["completed"] = completed

	var terminated int
	err = r.db.Get(&terminated, "SELECT COUNT(*) FROM public.process_instances WHERE status = 'terminated'")
	if err != nil {
		return nil, err
	}
	stats["terminated"] = terminated

	var suspended int
	err = r.db.Get(&suspended, "SELECT COUNT(*) FROM public.process_instances WHERE status = 'suspended'")
	if err != nil {
		return nil, err
	}
	stats["suspended"] = suspended

	return stats, nil
}

// Execution represents an execution record
type Execution struct {
	ID                 int64     `db:"engine_key"`
	ProcessInstanceID  uuid.UUID `db:"process_instance_id"`
	ProcessInstanceKey int64     `db:"process_instance_key"`
	CurrentElementID   string    `db:"current_element_id"`
	Status             string    `db:"status"`
	ParentExecutionID  *int64    `db:"parent_execution_key"`
	PathID             *string   `db:"path_id"`
	IsActive           bool      `db:"is_active"`
	CreatedAt          time.Time `db:"created_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}
