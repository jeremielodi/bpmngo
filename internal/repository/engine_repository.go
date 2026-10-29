package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/events"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

// Custom errors
var (
	ErrInvalidOutgoingFlows = errors.New("node has invalid number of outgoing flows")
	ErrNodeNotFound         = errors.New("node not found in graph")
)

// graphCache holds deserialized ProcessGraph values keyed by process_definition_id
// string — the graph is a property of the definition, not the instance, so every
// instance of the same definition shares one cache entry instead of duplicating
// an identical graph per instance. instanceToDefCache is a small, cheap
// instanceID->definitionID lookup so a cache hit still costs zero DB round
// trips (mirrors what the old instance-keyed cache gave for free). Both are
// bounded like internal/common/condition.go's compilationCache: evict one
// arbitrary entry on overflow rather than growing forever.
const (
	graphCacheMaxEntries       = 500
	instanceDefCacheMaxEntries = 5000
)

var (
	graphCacheMu       sync.RWMutex
	graphCache         = make(map[string]*engine.ProcessGraph)
	instanceToDefCache = make(map[string]string)
)

// ClearGraphCache clears the process-graph cache. Mirrors condition.go's
// ClearConditionCache; mainly useful for tests.
func ClearGraphCache() {
	graphCacheMu.Lock()
	defer graphCacheMu.Unlock()
	graphCache = make(map[string]*engine.ProcessGraph)
	instanceToDefCache = make(map[string]string)
}

type EngineRepository struct {
	db         *sqlx.DB
	dispatcher *events.TaskEventDispatcher
}

func NewEngineRepository(db *sqlx.DB, dispatcher *events.TaskEventDispatcher) *EngineRepository {
	return &EngineRepository{db: db, dispatcher: dispatcher}
}

// GetActiveExecution retrieves an active execution by ID
func (r *EngineRepository) GetActiveExecution(ctx context.Context, execID int64) (*Execution, error) {
	var exec Execution
	query := `
		SELECT e.engine_key, pi.id AS process_instance_id, e.process_instance_key, e.current_element_id, e.status,
		       e.is_active, e.parent_execution_key, e.path_id, e.created_at, e.updated_at
		FROM public.executions e
		JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		WHERE e.engine_key = $1 AND e.is_active = true
	`
	err := r.db.GetContext(ctx, &exec, query, execID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	RememberProcessInstanceKey(exec.ProcessInstanceID, exec.ProcessInstanceKey)
	return &exec, nil
}

// CreateTimerJob creates a new timer job
func (r *EngineRepository) CreateTimerJob(timer *models.TimerJob) error {
	instanceKey, err := processInstanceEngineKey(r.db, timer.ProcessInstanceID)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		INSERT INTO public.timer_jobs 
		(engine_key, process_instance_key, execution_key, event_type, due_at, payload, is_triggered, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, false, NOW())
	`, timer.ID, instanceKey, timer.ExecutionID, timer.EventType, timer.DueAt, timer.Payload)
	if err != nil {
		return err
	}
	return NotifyTimerArmedTx(r.db)
}

// GetExecutionByID retrieves an execution by ID regardless of active status
func (r *EngineRepository) GetExecutionByID(execID int64) (*Execution, error) {
	var exec Execution
	query := `
		SELECT e.engine_key, pi.id AS process_instance_id, e.process_instance_key, e.current_element_id, e.status,
		       e.is_active, e.parent_execution_key, e.path_id, e.created_at, e.updated_at
		FROM public.executions e
		JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		WHERE e.engine_key = $1
	`
	err := r.db.Get(&exec, query, execID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	RememberProcessInstanceKey(exec.ProcessInstanceID, exec.ProcessInstanceKey)
	return &exec, nil
}

func (r *EngineRepository) CountGatewayWaitingState(processInstanceID uuid.UUID, nodeID string) int {
	var count int
	query := `
		SELECT COUNT(*) 
		FROM public.gateway_state 
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND gateway_id = $2 
		  AND status = 'waiting'
	`
	r.db.Get(&count, query, processInstanceID, nodeID)
	return count
}

// GetProcessVariables retrieves variables for a process instance
func (r *EngineRepository) GetProcessVariables(instanceID uuid.UUID) (map[string]interface{}, error) {
	var data []byte
	err := r.db.Get(&data, `
		SELECT v.data FROM public.variables v
		JOIN public.process_instances pi ON pi.engine_key = v.process_instance_key
		WHERE pi.id = $1
	`, instanceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	variables := make(map[string]interface{})
	if len(data) > 0 {
		if err := json.Unmarshal(data, &variables); err != nil {
			return nil, err
		}
	}
	return variables, nil
}

// CreateGatewayStateTx inserts a new gateway_state row, returning created =
// false (no error) if a concurrent branch already created one for this
// (process_instance_id, gateway_id) pair first. That race is real and
// pre-existing — the caller's own "does a waiting gateway_state already
// exist" check and this insert aren't atomic — just narrow enough on a
// single low-latency Postgres to have never been observed in practice
// until Citus's added coordinator/worker network hop widened it enough to
// reproduce reliably (found via Citus phase 2 testing, see
// architecture/citus-distributed-storage-design.md). Callers must treat
// created = false as "join the row a concurrent branch just created",
// not an error.
func (r *EngineRepository) CreateGatewayStateTx(tx *sqlx.Tx, state *models.GatewayState) (created bool, err error) {
	instanceKey, err := processInstanceEngineKey(tx, state.ProcessInstanceID)
	if err != nil {
		return false, err
	}
	state.ProcessInstanceKey = instanceKey
	res, err := tx.Exec(`
		INSERT INTO public.gateway_state
		(id, process_instance_key, gateway_id, expected_incoming, received_incoming, joined_flows, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (process_instance_key, gateway_id) DO NOTHING
	`, state.ID, instanceKey, state.GatewayID,
		state.ExpectedIncoming, state.ReceivedIncoming, state.JoinedFlows, state.Status)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// CreateTimerJobTx creates a new timer job within a transaction
func (r *EngineRepository) CreateTimerJobTx(tx *sqlx.Tx, timer *models.TimerJob) error {
	instanceKey, err := processInstanceEngineKey(tx, timer.ProcessInstanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO public.timer_jobs 
		(engine_key, process_instance_key, execution_key, event_type, due_at, payload, is_triggered, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, timer.ID, instanceKey, timer.ExecutionID, timer.EventType, timer.DueAt, timer.Payload, false, time.Now())
	if err != nil {
		return err
	}
	return NotifyTimerArmedTx(tx)
}
func (r *EngineRepository) CreateTimerTx(tx *sqlx.Tx, timer *models.TimerJob) error {
	instanceKey, err := processInstanceEngineKey(tx, timer.ProcessInstanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
        INSERT INTO public.timer_jobs 
        (engine_key, process_instance_key, execution_key, event_type, due_at, payload,
         is_triggered, created_at, cycle_count, total_cycles, repeat_interval)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, timer.ID, instanceKey, timer.ExecutionID,
		timer.EventType, timer.DueAt, timer.Payload, timer.IsTriggered,
		timer.CreatedAt, timer.CycleCount, timer.TotalCycles, timer.RepeatInterval)
	if err != nil {
		return err
	}
	return NotifyTimerArmedTx(tx)
}

// GetDueTimers retrieves all timers that are due to be triggered (legacy, no locking).
func (r *EngineRepository) GetDueTimers(ctx context.Context, now time.Time) ([]models.TimerJob, error) {
	var timers []models.TimerJob
	query := `
        SELECT t.engine_key, pi.id AS process_instance_id, t.execution_key, t.event_type, t.due_at, t.payload,
               t.is_triggered, t.created_at, t.triggered_at, t.cycle_count, t.total_cycles, t.repeat_interval
        FROM public.timer_jobs t
        JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
        WHERE t.is_triggered = false AND t.due_at <= NOW()
        ORDER BY t.due_at ASC
        LIMIT 100
    `
	err := r.db.SelectContext(ctx, &timers, query)
	if err != nil {
		return nil, err
	}
	for _, t := range timers {
		log.Printf("   - Timer %d: due_at=%s, triggered=%v", t.ID, t.DueAt, t.IsTriggered)
	}
	return timers, err
}

// ClaimDueTimers atomically marks due timers as triggered and returns them.
// Uses FOR UPDATE SKIP LOCKED so concurrent scheduler instances never double-fire.
func (r *EngineRepository) ClaimDueTimers(ctx context.Context, limit int) ([]models.TimerJob, error) {
	if distributedClaims.Load() {
		return claimDueTimers(ctx, r.db, limit)
	}
	var timers []models.TimerJob
	query := `
        WITH to_claim AS (
            SELECT engine_key FROM public.timer_jobs
            WHERE is_triggered = false AND due_at <= NOW()
            ORDER BY due_at ASC
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        )
        UPDATE public.timer_jobs t
        SET is_triggered = true, triggered_at = NOW()
        FROM to_claim
		WHERE t.engine_key = to_claim.engine_key
		RETURNING t.engine_key, (SELECT id FROM public.process_instances WHERE engine_key = t.process_instance_key) AS process_instance_id,
		          t.execution_key, t.event_type, t.due_at, t.payload, t.is_triggered, t.created_at, t.triggered_at,
		          COALESCE(t.cycle_count, 0) AS cycle_count, COALESCE(t.total_cycles, 1) AS total_cycles,
		          COALESCE(t.repeat_interval, '') AS repeat_interval
    `
	err := r.db.SelectContext(ctx, &timers, query, limit)
	if err != nil {
		return nil, err
	}
	for _, t := range timers {
		log.Printf("   ✅ Claimed timer %d due_at=%s", t.ID, t.DueAt)
	}
	return timers, nil
}

func (r *EngineRepository) CreateTimerWithDurationSeconds(tx *sqlx.Tx, timerID int64, processInstanceID uuid.UUID, executionID *int64, durationSeconds int, eventType string, payload []byte, cycleCount, totalCycles int, repeatInterval string) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
        INSERT INTO public.timer_jobs 
        (engine_key, process_instance_key, execution_key, event_type, due_at, payload,
         is_triggered, created_at, cycle_count, total_cycles, repeat_interval)
		VALUES ($1, $2, $3, $4, NOW() + ($5 || ' seconds')::interval, $6, false, NOW(), $7, $8, $9)
	`, timerID, instanceKey, executionID, eventType, durationSeconds, payload, cycleCount, totalCycles, repeatInterval)
	if err != nil {
		return err
	}
	return NotifyTimerArmedTx(tx)
}

// GetMultiInstanceChildrenCount returns the count of completed child executions
func (r *EngineRepository) GetMultiInstanceChildrenCount(parentExecutionID int64, status string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM public.multi_instance_children WHERE parent_execution_key = $1`
	if status != "" {
		query += ` AND status = $2`
		err := r.db.Get(&count, query, parentExecutionID, status)
		return count, err
	}
	err := r.db.Get(&count, query, parentExecutionID)
	return count, err
}

// CompleteMultiInstanceChild marks a child execution as completed
func (r *EngineRepository) CompleteMultiInstanceChildTx(tx *sqlx.Tx, childExecutionID int64) error {
	_, err := tx.Exec(`
        UPDATE public.multi_instance_children 
        SET status = 'completed', completed_at = NOW() 
        WHERE child_execution_key = $1
    `, childExecutionID)
	return err
}

// GetMultiInstanceExecution retrieves a multi-instance execution by execution ID
func (r *EngineRepository) GetMultiInstanceExecution(executionID int64) (*models.MultiInstanceExecution, error) {
	var miExec models.MultiInstanceExecution
	query := `
		SELECT mi.id, pi.id AS process_instance_id, mi.execution_key, mi.activity_id, mi.is_sequential, mi.total_count,
               completed_count, nr_of_active_instances, nr_of_completed_instances, loop_counter, 
               completion_condition, element_variable, input_collection, output_collection, status, 
               created_at, updated_at 
		FROM public.multi_instance_executions mi
		JOIN public.process_instances pi ON pi.engine_key = mi.process_instance_key
		WHERE mi.execution_key = $1 AND mi.status = 'active'
    `
	err := r.db.Get(&miExec, query, executionID)
	if err != nil {
		return nil, err
	}
	return &miExec, nil
}

func (r *EngineRepository) GetMultiInstanceExecutionByParentId(parentExecID int64) (*models.MultiInstanceExecution, error) {
	var mi models.MultiInstanceExecution
	err := r.db.Get(&mi, `SELECT * FROM public.multi_instance_executions WHERE execution_key = $1 AND status = 'active'`, parentExecID)
	return &mi, err
}

// GetDueTimersByInstance retrieves due timers for a specific process instance
func (r *EngineRepository) GetDueTimersByInstance(instanceID uuid.UUID, now time.Time) ([]models.TimerJob, error) {
	var timers []models.TimerJob

	query := `
		SELECT t.engine_key, pi.id AS process_instance_id, t.execution_key, t.event_type, t.due_at,
		       t.payload, t.is_triggered, t.created_at, t.triggered_at
		FROM public.timer_jobs t
		JOIN public.process_instances pi ON pi.engine_key = t.process_instance_key
		WHERE pi.id = $1 AND t.is_triggered = false AND t.due_at <= $2
		ORDER BY t.due_at ASC
	`

	err := r.db.Select(&timers, query, instanceID, now)
	return timers, err
}

// MarkTimerFired marks a timer as triggered
func (r *EngineRepository) MarkTimerFired(ctx context.Context, timerID int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE public.timer_jobs
		SET is_triggered = true, triggered_at = NOW()
		WHERE engine_key = $1
	`, timerID)
	return err
}

// MarkTimerFiredTx marks a timer as triggered within a transaction
func (r *EngineRepository) MarkTimerFiredTx(tx *sqlx.Tx, timerID int64) error {
	_, err := tx.Exec(`
		UPDATE public.timer_jobs
		SET is_triggered = true, triggered_at = NOW()
		WHERE engine_key = $1
	`, timerID)
	return err
}

// DeleteTimer removes a timer job
func (r *EngineRepository) DeleteTimerTx(tx *sqlx.Tx, timerID int64) error {
	_, err := tx.Exec(`DELETE FROM public.timer_jobs WHERE engine_key = $1`, timerID)
	return err
}

// DeleteTimersByExecution removes all timers for a specific execution
func (r *EngineRepository) DeleteTimersByExecutionTx(tx *sqlx.Tx, executionID int64) error {
	_, err := tx.Exec(`DELETE FROM public.timer_jobs WHERE execution_key = $1`, executionID)
	return err
}

// DeleteTimersByProcessInstanceTx removes every not-yet-triggered timer for
// a process instance — used when the whole instance is terminated, so the
// timer scheduler never fires a timer belonging to a process that no longer
// runs. Already-triggered timers are left alone (their history is moot but
// harmless to keep).
func (r *EngineRepository) DeleteTimersByProcessInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		DELETE FROM public.timer_jobs
		WHERE process_instance_key = $1
		  AND is_triggered = false
	`, instanceKey)
	return err
}

// CancelPendingJobsByProcessInstanceTx cancels every open job of a process
// instance — used when the whole instance is terminated, so no worker can
// fetch-and-lock a job belonging to a process that no longer runs, nor
// complete one it already holds (that answer is then NOT_FOUND, as in
// Camunda 8). Returns the cancelled job IDs so the caller can audit-log
// each one.
func (r *EngineRepository) CancelPendingJobsByProcessInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID) ([]int64, error) {
	var cancelledJobIDs []int64
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return nil, err
	}
	err = tx.Select(&cancelledJobIDs, `
		UPDATE public.jobs SET status = 'cancelled', updated_at = NOW()
		WHERE process_instance_key = $1
		  AND status IN ('pending', 'locked')
		RETURNING engine_key
	`, instanceKey)
	return cancelledJobIDs, err
}

// CancelUserTaskByExecution cancels any active user task for an execution
func (r *EngineRepository) CancelUserTaskByExecutionTx(tx *sqlx.Tx, executionID int64) error {
	_, err := tx.Exec(`
		UPDATE public.tasks
		SET status = 'cancelled', updated_at = NOW()
		WHERE execution_key = $1 AND status = 'created'
	`, executionID)
	return err
}

// internal/repository/engine_repository.go

// CreateTimerWithCycleTx creates a timer job with cycle support
func (r *EngineRepository) CreateTimerWithCycleTx(tx *sqlx.Tx, timerID int64, processInstanceID uuid.UUID, executionID *int64, dueAt time.Time, eventType string, payload []byte, cycleCount, totalCycles int, repeatInterval string) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
        INSERT INTO public.timer_jobs 
		(engine_key, process_instance_key, execution_key, event_type, due_at, payload, is_triggered, created_at, cycle_count, total_cycles, repeat_interval)
		VALUES ($1, $2, $3, $4, $5, $6, false, NOW(), $7, $8, $9)
	    `, timerID, instanceKey, executionID, eventType, dueAt, payload, cycleCount, totalCycles, repeatInterval)
	if err != nil {
		return err
	}
	return NotifyTimerArmedTx(tx)
}

// CompleteUserTaskByExecution completes any active user task for an execution
func (r *EngineRepository) CompleteUserTaskByExecutionTx(tx *sqlx.Tx, executionID int64) error {
	_, err := tx.Exec(`
		UPDATE public.tasks
		SET status = 'completed', updated_at = NOW()
		WHERE execution_key = $1 AND status = 'created'
	`, executionID)
	return err
}

// GetChildExecutions gets all child executions for a parent
func (r *EngineRepository) GetChildExecutions(parentExecID int64) ([]Execution, error) {
	var executions []Execution
	err := r.db.Select(&executions, `
		SELECT e.engine_key, pi.id AS process_instance_id, e.process_instance_key, e.current_element_id, e.status, e.is_active, e.path_id
		FROM public.executions e
		JOIN public.process_instances pi ON pi.engine_key = e.process_instance_key
		WHERE e.parent_execution_key = $1 AND e.is_active = true
	`, parentExecID)
	return executions, err
}

// CompleteChildExecution marks a child execution as completed
func (r *EngineRepository) CompleteChildExecutionTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		UPDATE public.executions SET status = 'completed', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1 AND engine_key = $2
	`, instanceKey, execID)
	return err
}

// UpdateExecutionNode updates the current node of an execution
func (r *EngineRepository) UpdateExecutionNodeTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64, nodeID string) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.UpdateExecutionNodeByKeyTx(tx, instanceKey, execID, nodeID)
}

// UpdateExecutionNodeByKeyTx is the runtime hot-path variant. Executions
// already carry their owning process_instance_key, so resolving the public
// UUID again would add one SQL round trip for every BPMN step.
func (r *EngineRepository) UpdateExecutionNodeByKeyTx(tx *sqlx.Tx, instanceKey, execID int64, nodeID string) error {
	_, err := tx.Exec(`
		UPDATE public.executions SET current_element_id = $1, updated_at = NOW()
		WHERE process_instance_key = $2 AND engine_key = $3
	`, nodeID, instanceKey, execID)
	return err
}

// UpdateExecutionStatus updates the status of an execution
func (r *EngineRepository) UpdateExecutionStatusTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64, status string) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.UpdateExecutionStatusByKeyTx(tx, instanceKey, execID, status)
}

func (r *EngineRepository) UpdateExecutionStatusByKeyTx(tx *sqlx.Tx, instanceKey, execID int64, status string) error {
	_, err := tx.Exec(`
		UPDATE public.executions SET status = $1, updated_at = NOW()
		WHERE process_instance_key = $2 AND engine_key = $3
	`, status, instanceKey, execID)
	return err
}

// UpdateExecutionStatusAndNode updates both status and current node
func (r *EngineRepository) UpdateExecutionStatusAndNodeTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64, status, nodeID string) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.UpdateExecutionStatusAndNodeByKeyTx(tx, instanceKey, execID, status, nodeID)
}

func (r *EngineRepository) UpdateExecutionStatusAndNodeByKeyTx(tx *sqlx.Tx, instanceKey, execID int64, status, nodeID string) error {
	_, err := tx.Exec(`
		UPDATE public.executions SET status = $1, current_element_id = $2, updated_at = NOW()
		WHERE process_instance_key = $3 AND engine_key = $4
	`, status, nodeID, instanceKey, execID)
	return err
}

// CompleteExecution marks an execution as completed
func (r *EngineRepository) CompleteExecutionTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.CompleteExecutionByKeyTx(tx, instanceKey, execID)
}

func (r *EngineRepository) CompleteExecutionByKeyTx(tx *sqlx.Tx, instanceKey, execID int64) error {
	_, err := tx.Exec(`
		UPDATE public.executions SET status = 'completed', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1 AND engine_key = $2
	`, instanceKey, execID)
	return err
}

// CreateJob creates a new service task job
func (r *EngineRepository) CreateJobTx(tx *sqlx.Tx, jobID int64, processInstanceID uuid.UUID, executionID int64, jobType string, payload []byte) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.CreateJobByKeyTx(tx, jobID, instanceKey, executionID, jobType, payload)
}

func (r *EngineRepository) CreateJobByKeyTx(tx *sqlx.Tx, jobID, instanceKey, executionID int64, jobType string, payload []byte) error {
	_, err := tx.Exec(`
		INSERT INTO public.jobs
		(engine_key, process_instance_key, execution_key, job_type, status, payload, retries, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, 3, NOW(), NOW())
	`, jobID, instanceKey, executionID, jobType, payload)
	return err
}

type UserTask struct {
	ID                int64
	ProcessInstanceID uuid.UUID
	ExecutionID       int64
	TaskDefinitionKey string
	TaskName          *string
	Assignee          *string
	CandidateGroup    *string
	Priority          int
}

// CreateUserTask creates a new user task
func (r *EngineRepository) CreateUserTaskTx(tx *sqlx.Tx, task *UserTask) error {
	priority := task.Priority
	if priority == 0 {
		priority = 50
	}
	instanceKey, err := processInstanceEngineKey(tx, task.ProcessInstanceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO public.tasks
		(engine_key, process_instance_key, execution_key, task_definition_key, task_name, assignee, candidate_group, priority, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'created', NOW(), NOW())
	`, task.ID, instanceKey, task.ExecutionID, task.TaskDefinitionKey,
		task.TaskName, task.Assignee, task.CandidateGroup, priority)
	return err
}

// repository/engine_repository.go

// CreateProcessInstanceTx creates a new process instance within a
// transaction. tenantID is stamped from the process definition being
// instantiated (nil = no tenant / untenanted deployment).
func (r *EngineRepository) CreateProcessInstanceTx(tx *sqlx.Tx, instanceID, processDefinitionID uuid.UUID, startedAt time.Time, tenantID *string) error {
	return r.CreateProcessInstanceByKeyTx(tx, instanceID, ids.NewEngineKey(), processDefinitionID, startedAt, tenantID)
}

func (r *EngineRepository) CreateProcessInstanceByKeyTx(tx *sqlx.Tx, instanceID uuid.UUID, instanceKey int64, processDefinitionID uuid.UUID, startedAt time.Time, tenantID *string) error {
	definitionKey, err := processDefinitionEngineKey(tx, processDefinitionID)
	if err != nil {
		return err
	}
	query := `
        INSERT INTO public.process_instances
		(id, engine_key, process_definition_id, process_definition_key, status, started_at, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	// Use 'running' from the enum
	_, err = tx.Exec(query, instanceID, instanceKey, processDefinitionID, definitionKey, "running", startedAt, tenantID)
	return err
}

// UpdateProcessInstanceStatusTx updates the status of a process instance
func (r *EngineRepository) UpdateProcessInstanceStatusTx(tx *sqlx.Tx, instanceID uuid.UUID, status string, endedAt *time.Time) error {
	if endedAt != nil {
		_, err := tx.Exec(`UPDATE public.process_instances SET status = $1, ended_at = $2 WHERE id = $3`, status, *endedAt, instanceID)
		return err
	}
	_, err := tx.Exec(`UPDATE public.process_instances SET status = $1 WHERE id = $2`, status, instanceID)
	return err
}

// CompleteProcessInstance marks a process instance as completed
func (r *EngineRepository) CompleteProcessInstanceTx(tx *sqlx.Tx, instanceID uuid.UUID) error {
	if err := r.UpdateProcessInstanceStatusTx(tx, instanceID, "completed", &[]time.Time{time.Now()}[0]); err != nil {
		return err
	}
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	// A finished instance receives nothing more: its subscriptions are
	// closed, its pending timers dropped and its still-armed trigger
	// executions (event sub-processes, boundary events) ended — right now,
	// not when it gets archived (in the background, a moment later).
	if _, err := tx.Exec(`
		UPDATE public.event_subscriptions SET consumed_at = NOW()
		WHERE process_instance_key = $1 AND consumed_at IS NULL
	`, instanceKey); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM public.timer_jobs WHERE process_instance_key = $1 AND is_triggered = false`, instanceKey); err != nil {
		return err
	}
	_, err = tx.Exec(`
		UPDATE public.executions SET status = 'completed', is_active = false, updated_at = NOW()
		WHERE process_instance_key = $1 AND is_active = true
	`, instanceKey)
	return err
}

// // CreateExecutionTx creates a new execution within a transaction
// func (r *EngineRepository) CreateExecutionTx(tx *sqlx.Tx, execID, processInstanceID uuid.UUID, currentElementID string, createdAt time.Time) error {
// 	query := `
//         INSERT INTO public.executions
//         (id, process_instance_id, current_element_id, status, is_active, created_at, updated_at)
//         VALUES ($1, $2, $3, $4, $5, $6, $7)
//     `
// 	// Use 'active' from execution_status enum
// 	_, err := tx.Exec(query, execID, processInstanceID, currentElementID, "active", true, createdAt, createdAt)
// 	return err
// }

// UpdateProcessVariables updates variables for a process instance
func (r *EngineRepository) UpdateProcessVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, variables map[string]interface{}) error {
	jsonData, err := json.Marshal(variables)
	if err != nil {
		return err
	}
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}

	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM public.variables WHERE process_instance_key = $1)`, instanceKey).Scan(&exists); err != nil {
		return err
	}

	if exists {
		_, err = tx.Exec(`UPDATE public.variables SET data = $1, updated_at = NOW() WHERE process_instance_key = $2`, jsonData, instanceKey)
	} else {
		_, err = tx.Exec(`INSERT INTO public.variables (engine_key, process_instance_key, data, updated_at) VALUES ($1, $2, $3, NOW())`, ids.NewEngineKey(), instanceKey, jsonData)
	}
	return notifyVariables(tx, instanceID, err)
}

// MergeProcessVariables merges new variables with existing ones
func (r *EngineRepository) MergeProcessVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, newVars map[string]interface{}) error {
	// Get existing variables
	existingVars, err := r.GetProcessVariables(instanceID)
	if err != nil {
		return err
	}

	// Merge
	for k, v := range newVars {
		existingVars[k] = v
	}

	// Save merged variables
	return r.UpdateProcessVariablesTx(tx, instanceID, existingVars)
}

// MarkTimerTriggered marks a timer as triggered
func (r *EngineRepository) MarkTimerTriggeredTx(tx *sqlx.Tx, timerID uuid.UUID) error {
	_, err := tx.Exec(`
		UPDATE public.timer_jobs
		SET is_triggered = true, triggered_at = NOW()
		WHERE id = $1
	`, timerID)
	return err
}

// GetProcessDefinitionByInstanceID retrieves the process definition for a process instance
func (r *EngineRepository) GetProcessDefinitionByInstanceID(instanceID uuid.UUID) (*models.ProcessDefinition, error) {
	var def models.ProcessDefinition
	query := `
		SELECT pd.id, pd.process_key, pd.version, pd.parsed_graph, pd.created_at
		FROM public.process_definitions pd
		JOIN public.process_instances pi ON pi.process_definition_id = pd.id
		WHERE pi.id = $1
	`
	err := r.db.Get(&def, query, instanceID)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// GetProcessGraphByInstanceID retrieves the process graph for a process instance.
// Results are cached in-process, deduplicated per process definition, to avoid
// repeated JSON deserialization.
func (r *EngineRepository) GetProcessGraphByInstanceID(instanceID uuid.UUID) (*engine.ProcessGraph, error) {
	instanceKey := instanceID.String()

	graphCacheMu.RLock()
	defID, haveDefID := instanceToDefCache[instanceKey]
	var cachedGraph *engine.ProcessGraph
	if haveDefID {
		cachedGraph = graphCache[defID]
	}
	graphCacheMu.RUnlock()
	if cachedGraph != nil {
		return cachedGraph, nil
	}

	var row struct {
		ParsedGraph         []byte    `db:"parsed_graph"`
		ProcessDefinitionID uuid.UUID `db:"process_definition_id"`
	}
	query := `
		SELECT pd.parsed_graph, pd.id AS process_definition_id
		FROM public.process_definitions pd
		JOIN public.process_instances pi ON pi.process_definition_id = pd.id
		WHERE pi.id = $1
	`
	if err := r.db.Get(&row, query, instanceID); err != nil {
		return nil, err
	}

	var graph engine.ProcessGraph
	if err := json.Unmarshal(row.ParsedGraph, &graph); err != nil {
		return nil, err
	}
	graphPtr := &graph

	defKey := row.ProcessDefinitionID.String()
	graphCacheMu.Lock()
	// Another instance of this same definition may already have populated
	// graphCache — reuse that pointer instead of overwriting it with the
	// one just parsed, so instances of one definition truly share a single
	// cached graph rather than the most-recently-queried instance winning.
	if existing, ok := graphCache[defKey]; ok {
		graphPtr = existing
	} else {
		if len(graphCache) >= graphCacheMaxEntries {
			for k := range graphCache {
				delete(graphCache, k)
				break
			}
		}
		graphCache[defKey] = graphPtr
	}
	if len(instanceToDefCache) >= instanceDefCacheMaxEntries {
		for k := range instanceToDefCache {
			delete(instanceToDefCache, k)
			break
		}
	}
	instanceToDefCache[instanceKey] = defKey
	graphCacheMu.Unlock()
	return graphPtr, nil
}

// GetNodeByID retrieves a node from the graph by ID
func (r *EngineRepository) GetNodeByID(graph *engine.ProcessGraph, nodeID string) (*engine.Node, error) {
	node, ok := graph.Nodes[nodeID]
	if !ok {
		return nil, ErrNodeNotFound
	}
	return node, nil
}

func (r *EngineRepository) CreateExecutionTx2(tx *sqlx.Tx, execID int64, processInstanceID uuid.UUID, currentElementID string, createdAt time.Time) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.CreateExecutionByKeyTx(tx, execID, instanceKey, currentElementID, createdAt)
}

func (r *EngineRepository) CreateExecutionByKeyTx(tx *sqlx.Tx, execID, instanceKey int64, currentElementID string, createdAt time.Time) error {
	query := `
        INSERT INTO public.executions 
		(engine_key, process_instance_key, current_element_id, status, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
    `
	_, err := tx.Exec(query, execID, instanceKey, currentElementID, "active", true, createdAt, createdAt)
	return err
}

// CreateExecutionTx creates a new execution within a transaction
func (r *EngineRepository) CreateExecutionTx(tx *sqlx.Tx, exec *Execution) error {
	instanceKey := exec.ProcessInstanceKey
	var err error
	if instanceKey == 0 {
		instanceKey, err = processInstanceEngineKey(tx, exec.ProcessInstanceID)
		if err != nil {
			return err
		}
	}
	query := `
		INSERT INTO public.executions
		(engine_key, process_instance_key, parent_execution_key, current_element_id, status, is_active, path_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
	`
	_, err = tx.Exec(query,
		exec.ID,
		instanceKey,
		exec.ParentExecutionID,
		exec.CurrentElementID,
		exec.Status,
		exec.IsActive,
		exec.PathID,
	)
	return err
}

// GetOutgoingFlows returns the outgoing flows from a node
func (r *EngineRepository) GetOutgoingFlows(node *engine.Node) []engine.Flow {
	return node.Outgoing
}

// HasMultipleOutgoingFlows checks if a node has multiple outgoing flows
func (r *EngineRepository) HasMultipleOutgoingFlows(node *engine.Node) bool {
	return len(node.Outgoing) > 1
}

// HasSingleOutgoingFlow checks if a node has a single outgoing flow
func (r *EngineRepository) HasSingleOutgoingFlow(node *engine.Node) bool {
	return len(node.Outgoing) == 1
}

// GetSingleOutgoingFlow returns the single outgoing flow from a node
func (r *EngineRepository) GetSingleOutgoingFlow(node *engine.Node) (*engine.Flow, error) {
	if len(node.Outgoing) != 1 {
		return nil, ErrInvalidOutgoingFlows
	}
	return &node.Outgoing[0], nil
}

// GetServiceTaskNode finds a service task node by ID
func (r *EngineRepository) GetServiceTaskNode(graph *engine.ProcessGraph, nodeID string) (*engine.Node, error) {
	node, ok := graph.Nodes[nodeID]
	if !ok {
		return nil, ErrNodeNotFound
	}
	if node.Type != engine.ServiceTaskType {
		return nil, errors.New("node is not a service task")
	}
	return node, nil
}

// CompleteJob marks a job as completed
func (r *EngineRepository) CompleteJobTx(tx *sqlx.Tx, jobID int64) error {
	_, err := tx.Exec(`
		UPDATE public.jobs
		SET status = 'completed', updated_at = NOW()
		WHERE engine_key = $1
	`, jobID)
	return err
}

// GetJobByID retrieves a job by ID
func (r *EngineRepository) GetJobByID(jobID int64) (*models.Job, error) {
	var job models.Job
	err := r.db.Get(&job, `
		SELECT j.engine_key, pi.id AS process_instance_id, j.execution_key, j.job_type, j.status,
		       j.payload, j.retries, j.created_at, j.updated_at
		FROM public.jobs j
		JOIN public.process_instances pi ON pi.engine_key = j.process_instance_key
		WHERE j.engine_key = $1
	`, jobID)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetAvailableJobsByTopic retrieves pending jobs for a topic
func (r *EngineRepository) GetAvailableJobsByTopic(topic string, limit int) ([]models.Job, error) {
	var jobs []models.Job
	err := r.db.Select(&jobs, `
		SELECT j.engine_key, pi.id AS process_instance_id, j.execution_key, j.job_type, j.status,
		       j.payload, j.retries, j.created_at, j.updated_at
		FROM public.jobs j
		JOIN public.process_instances pi ON pi.engine_key = j.process_instance_key
		WHERE j.job_type = $1 AND j.status = 'pending'
		ORDER BY j.created_at ASC
		LIMIT $2
	`, topic, limit)
	return jobs, err
}

// LockJob locks a job for a worker
func (r *EngineRepository) LockJob(jobID int64, workerID string, lockDuration int64) (bool, error) {
	result, err := r.db.Exec(`
		UPDATE public.jobs
		SET status = 'locked', locked_by = $1, locked_until = NOW() + ($2 * interval '1 millisecond'), updated_at = NOW()
		WHERE engine_key = $3 AND status = 'pending'
	`, workerID, lockDuration, jobID)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// Add these missing methods to repository/engine_repository.go

// ============================================================
// PROCESS DEFINITION METHODS
// ============================================================

// repository/engine_repository.go

// FindLatestProcessDefinitionByKeyForTenant: see the ProcessRepository
// method of that name ("" = no tenant: any version).
func (r *EngineRepository) FindLatestProcessDefinitionByKeyForTenant(processKey, tenant string) (*models.ProcessDefinition, error) {
	if tenant == "" {
		return r.FindLatestProcessDefinitionByKey(processKey)
	}
	var def models.ProcessDefinition
	if err := r.db.Get(&def, latestForTenantSQL, processKey, tenant); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("process definition not found for key: %s", processKey)
		}
		return nil, err
	}
	return &def, nil
}

func (r *EngineRepository) FindLatestProcessDefinitionByKey(processKey string) (*models.ProcessDefinition, error) {
	var def models.ProcessDefinition
	query := `
        SELECT id, deployment_id, process_key, process_name, tenant_id, version, is_active, bpmn_xml, parsed_graph, created_at, write_path
        FROM public.process_definitions
        WHERE process_key = $1 AND is_active = true
        ORDER BY version DESC
        LIMIT 1
    `
	err := r.db.Get(&def, query, processKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("process definition not found for key: %s", processKey)
		}
		return nil, err
	}
	return &def, nil
}

// ============================================================
// VARIABLE METHODS
// ============================================================

// CreateVariablesTx creates variables for a process instance within a transaction
func (r *EngineRepository) CreateVariablesTx(tx *sqlx.Tx, instanceID uuid.UUID, variables map[string]interface{}, updatedAt time.Time) error {
	if len(variables) == 0 {
		return nil
	}

	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	return r.CreateVariablesByKeyTx(tx, instanceID, instanceKey, variables, updatedAt)
}

func (r *EngineRepository) CreateVariablesByKeyTx(tx *sqlx.Tx, instanceID uuid.UUID, instanceKey int64, variables map[string]interface{}, updatedAt time.Time) error {
	if len(variables) == 0 {
		return nil
	}
	jsonData, err := json.Marshal(variables)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO public.variables 
		(engine_key, process_instance_key, data, updated_at)
		VALUES ($1, $2, $3, $4)
	`, ids.NewEngineKey(), instanceKey, jsonData, updatedAt)
	return notifyVariables(tx, instanceID, err)
}

// SetProcessVariableTx sets a single process variable within a transaction
func (r *EngineRepository) SetProcessVariableTx(tx *sqlx.Tx, instanceID uuid.UUID, key string, value interface{}) error {
	instanceKey, err := processInstanceEngineKey(tx, instanceID)
	if err != nil {
		return err
	}
	// Get existing variables
	var data []byte
	err = tx.Get(&data, `SELECT data FROM public.variables WHERE process_instance_key = $1`, instanceKey)

	vars := make(map[string]interface{})
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &vars); err != nil {
			return err
		}
	} else if err != nil && err != sql.ErrNoRows {
		return err
	}

	vars[key] = value

	newData, err := json.Marshal(vars)
	if err != nil {
		return err
	}

	if len(data) > 0 {
		_, err = tx.Exec(`UPDATE public.variables SET data = $1, updated_at = NOW() WHERE process_instance_key = $2`, newData, instanceKey)
	} else {
		_, err = tx.Exec(`INSERT INTO public.variables (engine_key, process_instance_key, data, updated_at) VALUES ($1, $2, $3, NOW())`,
			ids.NewEngineKey(), instanceKey, newData)
	}
	return notifyVariables(tx, instanceID, err)
}

// ============================================================
// PROCESS INSTANCE CREATION (for worker pool)
// ============================================================

// CreateProcessInstanceWithVariables creates a process instance and its variables in one transaction
func (r *EngineRepository) CreateProcessInstanceWithVariables(processKey string, variables map[string]interface{}) (uuid.UUID, int64, error) {
	// Get process definition
	procDef, err := r.FindLatestProcessDefinitionByKey(processKey)
	if err != nil {
		return uuid.Nil, 0, err
	}

	// Build graph
	graph, err := engine.BuildGraphForProcess([]byte(procDef.BpmnXML), processKey)
	if err != nil {
		return uuid.Nil, 0, err
	}

	// Find start node
	var startNodeID string
	for _, node := range graph.Nodes {
		if node.IsProcessStart() {
			startNodeID = node.ID
			break
		}
	}
	if startNodeID == "" {
		return uuid.Nil, 0, errors.New("no start event found")
	}

	// Start transaction
	tx, err := r.db.Beginx()
	if err != nil {
		return uuid.Nil, 0, err
	}
	defer tx.Rollback()

	// Create process instance
	instanceID := ids.NewGlobalID()
	instanceKey := ids.NewEngineKey()
	now := time.Now()
	err = r.CreateProcessInstanceByKeyTx(tx, instanceID, instanceKey, procDef.ID, now, procDef.TenantID)
	if err != nil {
		return uuid.Nil, 0, err
	}
	RememberProcessInstanceKey(instanceID, instanceKey)
	committed := false
	defer func() {
		if !committed {
			ForgetProcessInstanceKey(instanceID, instanceKey)
		}
	}()

	// Create variables
	if len(variables) > 0 {
		err = r.CreateVariablesByKeyTx(tx, instanceID, instanceKey, variables, now)
		if err != nil {
			return uuid.Nil, 0, err
		}
	}

	// Create execution
	execID := ids.NewEngineKey()
	err = r.CreateExecutionByKeyTx(tx, execID, instanceKey, startNodeID, now)
	if err != nil {
		return uuid.Nil, 0, err
	}

	if err := tx.Commit(); err != nil {
		return uuid.Nil, 0, err
	}
	committed = true

	return instanceID, execID, nil
}

// GetProcessGraphByDefinitionID returns a definition's graph, from the
// same cache as GetProcessGraphByInstanceID.
func (r *EngineRepository) GetProcessGraphByDefinitionID(definitionID uuid.UUID) (*engine.ProcessGraph, error) {
	defKey := definitionID.String()
	graphCacheMu.RLock()
	cached := graphCache[defKey]
	graphCacheMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	var raw []byte
	if err := r.db.Get(&raw, `SELECT parsed_graph FROM public.process_definitions WHERE id = $1`, definitionID); err != nil {
		return nil, err
	}
	var graph engine.ProcessGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return nil, err
	}

	graphCacheMu.Lock()
	defer graphCacheMu.Unlock()
	if existing, ok := graphCache[defKey]; ok {
		return existing, nil
	}
	if len(graphCache) >= graphCacheMaxEntries {
		for k := range graphCache {
			delete(graphCache, k)
			break
		}
	}
	graphCache[defKey] = &graph
	return &graph, nil
}
