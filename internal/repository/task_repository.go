// internal/repository/task_repository.go
package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/events"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/pkg/database"
	"github.com/jmoiron/sqlx"
)

type TaskRepository struct {
	db         *sqlx.DB
	dispatcher *events.TaskEventDispatcher
}

func NewTaskRepository(db *sqlx.DB, dispatcher *events.TaskEventDispatcher) *TaskRepository {
	return &TaskRepository{db: db, dispatcher: dispatcher}
}

// ============================================================
// CREATE
// ============================================================

// CreateTask inserts a new user task record.
// Expects a models.Task (with all fields except auto‑generated timestamps).
func (r *TaskRepository) CreateTask(task models.Task) (sql.Result, error) {
	adapter := database.NewDabaseAdapter(r.db)
	now := time.Now()
	id := ids.NewEngineKey()
	instanceKey, err := processInstanceEngineKey(r.db, task.ProcessInstanceID)
	if err != nil {
		return nil, err
	}

	// Convert form_data (if any) to JSONB
	var formDataJSON []byte
	if task.FormData != nil {
		formDataJSON, _ = json.Marshal(task.FormData)
	}

	// Set task ID for event
	task.ID = id
	task.Status = "created"
	task.CreatedAt = now

	result, err := adapter.Insert("public.tasks", []database.QueryParameter{
		{Key: "engine_key", Value: id},
		{Key: "process_instance_key", Value: instanceKey},
		{Key: "execution_key", Value: task.ExecutionID},
		{Key: "task_definition_key", Value: task.TaskDefinitionKey},
		{Key: "task_name", Value: task.TaskName},
		{Key: "assignee", Value: task.Assignee},
		{Key: "candidate_group", Value: task.CandidateGroup},
		{Key: "status", Value: task.Status},
		{Key: "form_data", Value: formDataJSON},
		{Key: "created_at", Value: now},
		{Key: "claimed_at", Value: task.ClaimedAt},
		{Key: "completed_at", Value: task.CompletedAt},
	})

	if err != nil {
		return result, err
	}

	// Dispatch TaskCreated event
	r.dispatchTaskEvent(id, &task, events.TaskCreated, nil)

	return result, nil
}

// ============================================================
// FIND / QUERY
// ============================================================

// FindTaskByID returns a single task by its engine key.
func (r *TaskRepository) FindTaskByID(id int64) (models.Task, error) {
	var task models.Task
	err := r.db.Get(&task, `
		SELECT
			t.engine_key,
			pi.id AS process_instance_id,
			t.execution_key,
			t.task_definition_key,
			t.task_name,
			t.assignee,
			t.candidate_group,
			t.status,
			t.priority,
			t.form_data,
			t.created_at,
			t.claimed_at,
			t.completed_at
		FROM public.tasks t
		JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE t.engine_key = $1
	`, id)
	return task, err
}

// FindTasksByAssignee returns all open tasks assigned to a specific user.
func (r *TaskRepository) FindTasksByAssignee(assignee string, status ...string) ([]models.Task, error) {
	var tasks []models.Task
	query := `
		SELECT
			t.engine_key,
			pi.id AS process_instance_id,
			t.execution_key,
			t.task_definition_key,
			t.task_name,
			t.assignee,
			t.candidate_group,
			t.status,
			t.priority,
			t.form_data,
			t.created_at,
			t.claimed_at,
			t.completed_at
		FROM public.tasks t
		JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE t.assignee = $1
	`
	args := []interface{}{assignee}
	if len(status) > 0 && status[0] != "" {
		query += " AND t.status = $2"
		args = append(args, status[0])
	}
	query += " ORDER BY t.created_at ASC"
	err := r.db.Select(&tasks, query, args...)
	return tasks, err
}

// FindAll returns tasks filtered by any column given in the params map.
// excludeStatuses, if given, are omitted from the result — but only when the
// caller didn't already filter explicitly by "status", so an explicit
// ?status=completed request still works.
func (r *TaskRepository) FindAll(params map[string]interface{}, excludeStatuses ...string) ([]models.Task, error) {
	allowedColumns := map[string]string{
		"id": "t.engine_key", "process_instance_id": "pi.id", "execution_id": "t.execution_key",
		"task_definition_key": "t.task_definition_key", "task_name": "t.task_name", "assignee": "t.assignee",
		"candidate_group": "t.candidate_group", "status": "t.status", "form_data": "t.form_data",
		"priority": "t.priority", "created_at": "t.created_at", "claimed_at": "t.claimed_at", "completed_at": "t.completed_at",
	}

	query := `
        SELECT
			t.engine_key,
			pi.id AS process_instance_id,
			t.execution_key,
	            t.task_definition_key,
	            t.task_name,
	            t.assignee,
	            t.candidate_group,
	            t.status,
	            t.priority,
	            t.form_data,
	            t.created_at,
	            t.claimed_at,
	            t.completed_at
		FROM public.tasks t
		JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
    `
	var conditions []string
	var args []interface{}
	i := 1
	for key, value := range params {
		if column, ok := allowedColumns[key]; ok {
			conditions = append(conditions, fmt.Sprintf("%s = $%d", column, i))
			args = append(args, value)
			i++
		}
	}
	if _, hasStatus := params["status"]; !hasStatus && len(excludeStatuses) > 0 {
		placeholders := make([]string, len(excludeStatuses))
		for j, s := range excludeStatuses {
			placeholders[j] = fmt.Sprintf("$%d", i)
			args = append(args, s)
			i++
		}
		conditions = append(conditions, fmt.Sprintf("t.status NOT IN (%s)", strings.Join(placeholders, ", ")))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY t.created_at ASC"

	var tasks []models.Task
	err := r.db.Select(&tasks, query, args...)
	return tasks, err
}

// FindTasksByCandidateGroup returns all open tasks for a given group.
func (r *TaskRepository) FindTasksByCandidateGroup(group string, status ...string) ([]models.Task, error) {
	var tasks []models.Task
	query := `
		SELECT
			t.engine_key,
			pi.id AS process_instance_id,
			t.execution_key,
			t.task_definition_key,
			t.task_name,
			t.assignee,
			t.candidate_group,
			t.status,
			t.priority,
			t.form_data,
			t.created_at,
			t.claimed_at,
			t.completed_at
		FROM public.tasks t
		JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE t.candidate_group = $1
	`
	args := []interface{}{group}
	if len(status) > 0 && status[0] != "" {
		query += " AND t.status = $2"
		args = append(args, status[0])
	}
	query += " ORDER BY t.created_at ASC"
	err := r.db.Select(&tasks, query, args...)
	return tasks, err
}

// FindTasksByProcessInstance returns all tasks belonging to a process instance.
func (r *TaskRepository) FindTasksByProcessInstance(instanceID uuid.UUID) ([]models.Task, error) {
	var tasks []models.Task
	err := r.db.Select(&tasks, `
		SELECT
			t.engine_key,
			pi.id AS process_instance_id,
			t.execution_key,
			t.task_definition_key,
			t.task_name,
			t.assignee,
			t.candidate_group,
			t.status,
			t.priority,
			t.form_data,
			t.created_at,
			t.claimed_at,
			t.completed_at
		FROM public.tasks t
		JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE pi.id = $1
		ORDER BY t.created_at DESC
	`, instanceID)
	return tasks, err
}

// ============================================================
// UPDATE / STATE CHANGE
// ============================================================

// ClaimTask assigns a task to a user and updates status to 'claimed'.
func (r *TaskRepository) ClaimTask(taskID int64, assignee string) (sql.Result, error) {
	now := time.Now()

	// Get task before update for event
	taskBefore, err := r.FindTaskByID(taskID)
	if err != nil {
		return nil, err
	}

	// Direct SQL rather than the DataBaseAdapter.Update helper (which only
	// supports a single WHERE condition) — process_instance_id is needed
	// alongside id for single-shard Citus routing.
	result, err := r.db.Exec(`
		UPDATE public.tasks
		SET assignee = $1, status = 'claimed', claimed_at = $2
		WHERE engine_key = $3
	`, assignee, now, taskID)

	if err != nil {
		return result, err
	}

	// Dispatch TaskClaimed event
	r.dispatchTaskEvent(taskID, &taskBefore, events.TaskClaimed, map[string]interface{}{
		"assignee": assignee,
	})

	return result, nil
}

// UnClaimTask removes assignee from a task and resets status to 'created'.
func (r *TaskRepository) UnClaimTask(taskID int64) (sql.Result, error) {
	// Get task before update for event
	taskBefore, err := r.FindTaskByID(taskID)
	if err != nil {
		return nil, err
	}

	// Direct SQL rather than the DataBaseAdapter.Update helper (which only
	// supports a single WHERE condition) — process_instance_id is needed
	// alongside id for single-shard Citus routing.
	result, err := r.db.Exec(`
		UPDATE public.tasks
		SET assignee = NULL, status = 'created', claimed_at = NULL
		WHERE engine_key = $1
	`, taskID)

	if err != nil {
		return result, err
	}

	r.dispatchTaskEvent(taskID, &taskBefore, events.TaskUnclaimed, nil)

	return result, nil
}

// UpdateTaskFormData merges new form data into the existing JSONB.
func (r *TaskRepository) UpdateTaskFormData(processInstanceID uuid.UUID, taskID int64, formData map[string]interface{}) (sql.Result, error) {
	newJSON, _ := json.Marshal(formData)
	_, err := r.db.Exec(`
		UPDATE public.tasks
		SET form_data = COALESCE(form_data, '{}'::jsonb) || $1::jsonb
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $2)
		  AND engine_key = $3
	`, newJSON, processInstanceID, taskID)
	if err != nil {
		return nil, err
	}
	return sql.Result(nil), nil
}

// ============================================================
// HELPERS
// ============================================================

// CancelOpenTasksByProcessInstanceTx cancels every open (created/claimed)
// task belonging to a process instance — used when the whole instance is
// terminated, so no task is left dangling in an "open" state for a process
// that no longer runs. Returns the cancelled task IDs so the caller can
// audit-log each one.
func (r *TaskRepository) CancelOpenTasksByProcessInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID) ([]int64, error) {
	var cancelledTaskIDs []int64
	err := tx.Select(&cancelledTaskIDs, `
		UPDATE public.tasks SET status = 'cancelled', updated_at = NOW()
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND status IN ('created', 'claimed')
		RETURNING engine_key
	`, instanceID)
	return cancelledTaskIDs, err
}

// ============================================================
// EVENT DISPATCHING
// ============================================================

// dispatchTaskEvent dispatches a task event to all registered listeners
func (r *TaskRepository) dispatchTaskEvent(taskID int64, task *models.Task, eventType events.TaskEventType, additionalData map[string]interface{}) {
	if r.dispatcher == nil {
		return
	}

	// Get process key from process definition
	processKey := ""
	if task.ProcessInstanceID != uuid.Nil {
		engineRepo := NewEngineRepository(r.db, r.dispatcher)
		procDef, err := engineRepo.GetProcessDefinitionByInstanceID(task.ProcessInstanceID)
		if err == nil && procDef != nil {
			processKey = procDef.ProcessKey
		}
	}

	// Get the new status based on event type
	newStatus := r.getStatusForEventType(eventType)

	event := &events.TaskEvent{
		ID:                uuid.New().String(),
		EventType:         eventType,
		TaskID:            strconv.FormatInt(taskID, 10),
		ProcessInstanceID: task.ProcessInstanceID.String(),
		ExecutionID:       optionalEngineKeyString(task.ExecutionID),
		TaskName:          task.TaskName,
		TaskDefinitionKey: task.TaskDefinitionKey,
		Assignee:          task.Assignee,
		CandidateGroup:    task.CandidateGroup,
		OldStatus:         &task.Status,
		NewStatus:         newStatus,
		Timestamp:         time.Now(),
		Variables:         additionalData,
		ProcessKey:        processKey,
	}

	// Add assignee to event if present in additional data
	if assignee, ok := additionalData["assignee"].(string); ok && assignee != "" {
		event.Assignee = &assignee
	}

	// Add reason if present
	if reason, ok := additionalData["reason"].(string); ok {
		event.Comment = reason
	}

	// Dispatch in goroutine to not block
	go r.dispatcher.Dispatch(context.Background(), event)
}

func optionalEngineKeyString(key *int64) string {
	if key == nil {
		return ""
	}
	return strconv.FormatInt(*key, 10)
}

// getStatusForEventType returns the status string for an event type
func (r *TaskRepository) getStatusForEventType(eventType events.TaskEventType) string {
	switch eventType {
	case events.TaskCreated:
		return "created"
	case events.TaskClaimed:
		return "claimed"
	case events.TaskUnclaimed:
		return "created"
	case events.TaskCompleted:
		return "completed"
	case events.TaskFailed:
		return "failed"
	case events.TaskCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}
