package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/pkg/database"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

type ExternalTaskRepository struct {
	db *sqlx.DB
}

func NewExternalTaskRepository(db *sqlx.DB) *ExternalTaskRepository {
	return &ExternalTaskRepository{db: db}
}

// FetchAndLockJobs fetches and locks pending jobs for given topics
func (r *ExternalTaskRepository) FetchAndLockJobsTx(tx *sqlx.Tx, topics []TopicConfig, workerID string, maxTasks int) ([]TopicJobs, error) {
	// A lost lock only means the job is fetched again (see NoSyncTx).
	if err := NoSyncTx(tx); err != nil {
		return nil, err
	}
	result := []TopicJobs{}

	for _, topic := range topics {
		// First, select and lock pending jobs — jobs alone, no join. Citus
		// phase 2 (see architecture/citus-distributed-storage-design.md)
		// made executions a distributed table; a FOR UPDATE query can't
		// join a distributed table unless it's routable to a single shard,
		// so the previous single query (jobs JOIN executions ... FOR
		// UPDATE) stopped working the moment executions was distributed —
		// found only by actually running the full regression against a
		// real Citus-backed stack. jobs itself stays local and
		// undistributed either way (see the design doc for why), so
		// locking it alone here is unaffected by any of this.
		var jobs []LockedJob

		lockedUntil := time.Now().Add(time.Duration(topic.LockDuration) * time.Millisecond)
		var err error
		if distributedClaims.Load() {
			jobs, err = claimJobsTx(tx, topic.TopicName, workerID, lockedUntil, maxTasks)
		} else {
			// Claim and return the jobs in one statement. The previous SELECT
			// FOR UPDATE followed by UPDATE held row locks while execution and
			// variable context was being assembled and doubled the hot-path
			// statements.
			err = tx.Select(&jobs, `
				WITH candidates AS (
					SELECT engine_key
					FROM public.jobs
					WHERE job_type = $1
					  AND status = 'pending'
					  AND (locked_until IS NULL OR locked_until < NOW())
					ORDER BY created_at, engine_key
					LIMIT $2
					FOR UPDATE SKIP LOCKED
				)
				UPDATE public.jobs j
				SET status = 'locked', locked_by = $3, locked_until = $4
				FROM candidates c
				WHERE j.engine_key = c.engine_key
				RETURNING j.engine_key, j.payload, j.retries, j.execution_key,
				          j.process_instance_key,
				          (SELECT id FROM public.process_instances
				           WHERE engine_key = j.process_instance_key) AS process_instance_id,
				          j.status, j.created_at
			`, topic.TopicName, maxTasks, workerID, lockedUntil)
		}
		if err != nil {
			return nil, err
		}

		if len(jobs) > 0 {
			result = append(result, TopicJobs{
				TopicName: topic.TopicName,
				Jobs:      jobs,
			})
		}
	}

	return result, nil
}

// HydrateLockedJobs loads all read-only job context after the short claim
// transaction has committed. Four batch reads replace the former per-job
// variable/scope lookups while no connection remains idle in a transaction.
func (r *ExternalTaskRepository) HydrateLockedJobs(topicJobs []TopicJobs) error {
	var jobs []*LockedJob
	instanceSet := map[int64]struct{}{}
	for ti := range topicJobs {
		for ji := range topicJobs[ti].Jobs {
			job := &topicJobs[ti].Jobs[ji]
			jobs = append(jobs, job)
			instanceSet[job.ProcessInstanceKey] = struct{}{}
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	instanceKeys := make([]int64, 0, len(instanceSet))
	for key := range instanceSet {
		instanceKeys = append(instanceKeys, key)
	}

	type executionRow struct {
		InstanceKey    int64   `db:"process_instance_key"`
		ID             int64   `db:"engine_key"`
		ParentID       *int64  `db:"parent_execution_key"`
		PathID         *string `db:"path_id"`
		CurrentElement string  `db:"current_element_id"`
	}
	var executions []executionRow
	if err := r.db.Select(&executions, `
		SELECT process_instance_key, engine_key, parent_execution_key, path_id, current_element_id
		FROM public.executions WHERE process_instance_key = ANY($1::bigint[])
	`, pq.Array(instanceKeys)); err != nil {
		return err
	}
	type variableRow struct {
		InstanceKey int64  `db:"process_instance_key"`
		Data        []byte `db:"data"`
	}
	var roots []variableRow
	if err := r.db.Select(&roots, `SELECT process_instance_key, data FROM public.variables WHERE process_instance_key = ANY($1::bigint[])`, pq.Array(instanceKeys)); err != nil {
		return err
	}
	type scopeRow struct {
		InstanceKey int64  `db:"process_instance_key"`
		ScopeKey    int64  `db:"scope_key"`
		Data        []byte `db:"data"`
	}
	var scopeRows []scopeRow
	if err := r.db.Select(&scopeRows, `SELECT process_instance_key, scope_key, data FROM public.scope_variables WHERE process_instance_key = ANY($1::bigint[])`, pq.Array(instanceKeys)); err != nil {
		return err
	}
	type instanceRow struct {
		ProcessInstanceKey   int64     `db:"process_instance_key"`
		ProcessInstanceID    uuid.UUID `db:"process_instance_id"`
		ProcessDefinitionID  uuid.UUID `db:"process_definition_id"`
		ProcessDefinitionKey int64     `db:"process_definition_key"`
		ProcessKey           string    `db:"process_key"`
		Version              int       `db:"version"`
		TenantID             *string   `db:"tenant_id"`
	}
	var instances []instanceRow
	if err := r.db.Select(&instances, `
		SELECT pi.engine_key AS process_instance_key, pi.id AS process_instance_id,
		       pi.process_definition_id, pd.engine_key AS process_definition_key,
		       pd.process_key, pd.version, pi.tenant_id
		FROM public.process_instances pi
		JOIN public.process_definitions pd ON pd.id = pi.process_definition_id
		WHERE pi.engine_key = ANY($1::bigint[])
	`, pq.Array(instanceKeys)); err != nil {
		return err
	}

	parents := map[int64]map[int64]*int64{}
	executionByID := map[int64]executionRow{}
	for _, e := range executions {
		if parents[e.InstanceKey] == nil {
			parents[e.InstanceKey] = map[int64]*int64{}
		}
		parents[e.InstanceKey][e.ID] = e.ParentID
		executionByID[e.ID] = e
	}
	rootVars := map[int64]map[string]interface{}{}
	for _, row := range roots {
		vars := map[string]interface{}{}
		if err := json.Unmarshal(row.Data, &vars); err != nil {
			return err
		}
		rootVars[row.InstanceKey] = vars
	}
	scopes := map[int64]map[int64]map[string]interface{}{}
	for _, row := range scopeRows {
		vars := map[string]interface{}{}
		if err := json.Unmarshal(row.Data, &vars); err != nil {
			return err
		}
		if scopes[row.InstanceKey] == nil {
			scopes[row.InstanceKey] = map[int64]map[string]interface{}{}
		}
		scopes[row.InstanceKey][row.ScopeKey] = vars
	}
	instanceByKey := map[int64]instanceRow{}
	for _, row := range instances {
		instanceByKey[row.ProcessInstanceKey] = row
	}

	for _, job := range jobs {
		exec := executionByID[job.ExecutionID]
		job.CurrentElementID = exec.CurrentElement
		ctx := instanceByKey[job.ProcessInstanceKey]
		job.ProcessInstanceID = ctx.ProcessInstanceID
		job.ProcessDefinitionID = ctx.ProcessDefinitionID
		job.ProcessDefinitionKey = ctx.ProcessDefinitionKey
		job.ProcessKey = ctx.ProcessKey
		job.ProcessVersion = ctx.Version
		job.TenantID = ctx.TenantID

		visible := make(map[string]interface{}, len(rootVars[job.ProcessInstanceKey]))
		for name, value := range rootVars[job.ProcessInstanceKey] {
			visible[name] = value
		}
		scopeID := job.ExecutionID
		if attached, ok := ParseBoundaryTriggerPathID(exec.PathID); ok {
			scopeID = attached
		}
		chain := make([]int64, 0, 4)
		seen := map[int64]bool{}
		for id := &scopeID; id != nil && !seen[*id]; id = parents[job.ProcessInstanceKey][*id] {
			seen[*id] = true
			chain = append(chain, *id)
		}
		for i := len(chain) - 1; i >= 0; i-- {
			for name, value := range scopes[job.ProcessInstanceKey][chain[i]] {
				visible[name] = value
			}
		}
		payload, err := json.Marshal(visible)
		if err != nil {
			return fmt.Errorf("job %s variables: %w", strconv.FormatInt(job.ID, 10), err)
		}
		job.Payload = payload
	}
	return nil
}

// GetJobWithExecution retrieves a job with its execution context
func (r *ExternalTaskRepository) GetJobWithExecutionTx(tx *sqlx.Tx, jobID int64) (*JobWithExecution, error) {
	var result JobWithExecution

	// The job row stays locked until tx ends: two completions (or a
	// completion and a failure) of one job can't both see it 'locked'. The
	// instance comes first so the locking query targets one instance —
	// the only kind of FOR UPDATE Citus accepts on a distributed table.
	var instanceKey int64
	if err := tx.Get(&instanceKey, `SELECT process_instance_key FROM public.jobs WHERE engine_key = $1`, jobID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	query := `
		SELECT
			j.execution_key,
			j.process_instance_key,
			pi.id AS process_instance_id,
			pd.parsed_graph,
			e.current_element_id,
			j.status,
			j.retries
		FROM public.jobs j
		JOIN public.executions e ON e.process_instance_key = j.process_instance_key AND e.engine_key = j.execution_key
		JOIN public.process_instances pi ON pi.engine_key = j.process_instance_key
		JOIN public.process_definitions pd ON pd.id = pi.process_definition_id
		WHERE j.process_instance_key = $1 AND j.engine_key = $2
		FOR UPDATE OF j
	`

	err := tx.QueryRowx(query, instanceKey, jobID).StructScan(&result)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	RememberProcessInstanceKey(result.ProcessInstanceID, result.ProcessInstanceKey)

	return &result, nil
}

// The job writes below name the job's process instance: on Citus that
// sends each to the one shard holding the job.

// CompleteJobTx marks a job as completed.
func (r *ExternalTaskRepository) CompleteJobTx(tx *sqlx.Tx, processInstanceID uuid.UUID, jobID int64) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.CompleteJobByKeyTx(tx, instanceKey, jobID)
}

func (r *ExternalTaskRepository) CompleteJobByKeyTx(tx *sqlx.Tx, instanceKey, jobID int64) error {
	_, err := tx.Exec(`
		UPDATE public.jobs SET status = 'completed', completed_at = NOW()
		WHERE process_instance_key = $1 AND engine_key = $2`, instanceKey, jobID)
	return err
}

// FailJobPermanentlyTx marks a job as failed with no more retries.
func (r *ExternalTaskRepository) FailJobPermanentlyTx(tx *sqlx.Tx, processInstanceID uuid.UUID, jobID int64, errorMessage string, errorCode string) error {
	_, err := tx.Exec(`
		UPDATE public.jobs
		SET status = 'failed', error_message = $1, error_code = $2, updated_at = NOW()
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $3)
		  AND engine_key = $4
	`, errorMessage, errorCode, processInstanceID, jobID)
	return err
}

// ResetJobForRetryTx puts a job back to pending with its remaining retries.
// With a backoff, locked_until holds it back until then: a pending job is
// claimable only once locked_until is NULL or past.
func (r *ExternalTaskRepository) ResetJobForRetryTx(tx *sqlx.Tx, processInstanceID uuid.UUID, jobID int64, retries int, errorMessage string, backoff time.Duration) error {
	var notBefore *time.Time
	if backoff > 0 {
		t := time.Now().Add(backoff)
		notBefore = &t
	}
	_, err := tx.Exec(`
		UPDATE public.jobs
		SET retries = $1, status = 'pending', locked_by = NULL, locked_until = $5, error_message = $2
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $3)
		  AND engine_key = $4
	`, retries, errorMessage, processInstanceID, jobID, notBefore)
	return err
}

// GetProcessDefinitionGraph retrieves the process graph for a process instance

// GetProcessDefinitionGraphTx retrieves the process graph for a process instance
func (r *ExternalTaskRepository) GetProcessDefinitionGraphTx(tx *sqlx.Tx, instanceID uuid.UUID) ([]byte, error) {
	var graphJSON []byte
	var err error

	query := `
        SELECT pd.parsed_graph
        FROM public.process_definitions pd
        JOIN public.process_instances pi ON pi.process_definition_id = pd.id
        WHERE pi.id = $1
    `

	if tx != nil {
		err = tx.Get(&graphJSON, query, instanceID)
	} else {
		err = r.db.Get(&graphJSON, query, instanceID)
	}

	if err != nil {
		return nil, err
	}

	return graphJSON, nil
}

// CompleteExecution marks an execution as completed. Uses direct SQL rather
// than the DataBaseAdapter.Update helper (which only supports a single
// WHERE condition) — process_instance_id is needed alongside id for
// single-shard Citus routing.
func (r *ExternalTaskRepository) CompleteExecutionTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64) error {
	_, err := tx.Exec(`
		UPDATE public.executions
		SET status = 'completed', is_active = false, updated_at = NOW()
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND engine_key = $2
	`, processInstanceID, execID)
	return err
}

// UpdateExecutionCurrentNode updates the current node of an execution. Same
// direct-SQL reasoning as CompleteExecutionTx above.
func (r *ExternalTaskRepository) UpdateExecutionCurrentNodeTx(tx *sqlx.Tx, processInstanceID uuid.UUID, execID int64, nextNodeID string) error {
	instanceKey, err := processInstanceEngineKey(tx, processInstanceID)
	if err != nil {
		return err
	}
	return r.UpdateExecutionCurrentNodeByKeyTx(tx, instanceKey, execID, nextNodeID)
}

func (r *ExternalTaskRepository) UpdateExecutionCurrentNodeByKeyTx(tx *sqlx.Tx, instanceKey, execID int64, nextNodeID string) error {
	_, err := tx.Exec(`
		UPDATE public.executions
		SET current_element_id = $1, status = 'active', updated_at = NOW()
		WHERE process_instance_key = $2 AND engine_key = $3
	`, nextNodeID, instanceKey, execID)
	return err
}

// GetServiceTaskNode checks if a node exists and returns it
func (r *ExternalTaskRepository) GetServiceTaskNode(graph *engine.ProcessGraph, nodeID string) (*engine.Node, error) {
	node, ok := graph.Nodes[nodeID]
	if !ok {
		return nil, ErrNodeNotFound
	}

	if node.Type != engine.ServiceTaskType {
		return nil, ErrNotServiceTask
	}

	return node, nil
}

// CreateJob creates a new external task job
func (r *ExternalTaskRepository) CreateJob(job *models.Job) (sql.Result, error) {
	adapter := database.NewDabaseAdapter(r.db)
	instanceKey, err := processInstanceEngineKey(r.db, job.ProcessInstanceID)
	if err != nil {
		return nil, err
	}

	return adapter.Insert("public.jobs", []database.QueryParameter{
		{Key: "engine_key", Value: job.ID},
		{Key: "job_type", Value: job.JobType},
		{Key: "status", Value: job.Status},
		{Key: "payload", Value: job.Payload},
		{Key: "retries", Value: job.Retries},
		{Key: "execution_key", Value: job.ExecutionID},
		{Key: "process_instance_key", Value: instanceKey},
		{Key: "created_at", Value: time.Now()},
	})
}

func (r *ExternalTaskRepository) GetNodeByID(graph *engine.ProcessGraph, nodeID string) (*engine.Node, error) {
	node, ok := graph.Nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not found in graph", nodeID)
	}
	return node, nil
}

// GetPendingJobsByType retrieves pending jobs by type
func (r *ExternalTaskRepository) GetPendingJobsByType(jobType string, limit int) ([]models.Job, error) {
	var jobs []models.Job

	query := `
		SELECT j.engine_key, j.job_type, j.status, j.payload, j.retries, j.execution_key,
		       pi.id AS process_instance_id, j.created_at
		FROM public.jobs j
		JOIN public.process_instances pi ON pi.engine_key = j.process_instance_key
		WHERE j.job_type = $1 AND j.status = 'pending'
		LIMIT $2
	`

	err := r.db.Select(&jobs, query, jobType, limit)
	return jobs, err
}

// TopicConfig represents a topic configuration for fetching jobs
type TopicConfig struct {
	TopicName    string
	LockDuration int // milliseconds
}

// TopicJobs represents jobs fetched for a specific topic
type TopicJobs struct {
	TopicName string
	Jobs      []LockedJob
}

// JobWithExecution represents a job joined with its execution context
type JobWithExecution struct {
	ExecutionID        int64     `db:"execution_key"`
	ProcessInstanceKey int64     `db:"process_instance_key"`
	ProcessInstanceID  uuid.UUID `db:"process_instance_id"`
	GraphJSON          []byte    `db:"parsed_graph"`
	CurrentElementID   string    `db:"current_element_id"`
	Status             string    `db:"status"`
	Retries            int       `db:"retries"`
}

// LockedJob represents a job with its execution context
type LockedJob struct {
	ID                   int64           `db:"engine_key"`
	Payload              json.RawMessage `db:"payload"`
	Retries              int             `db:"retries"`
	ExecutionID          int64           `db:"execution_key"`
	ProcessInstanceKey   int64           `db:"process_instance_key"`
	ProcessInstanceID    uuid.UUID       `db:"process_instance_id"`
	ProcessDefinitionID  uuid.UUID
	ProcessDefinitionKey int64
	ProcessKey           string
	ProcessVersion       int
	TenantID             *string
	CurrentElementID     string    `db:"current_element_id"`
	Status               string    `db:"status"`
	CreatedAt            time.Time `db:"created_at"`
}

// Custom errors
var (
	ErrNotServiceTask = errors.New("node is not a service task")
)

// ErrJobNotLockedBy: the job isn't locked by that worker (or not locked).
var ErrJobNotLockedBy = errors.New("external task is not locked by this worker")

// ExtendLock moves a locked job's lock expiry, if workerID holds it.
func (r *ExternalTaskRepository) ExtendLock(jobID int64, workerID string, until time.Time) error {
	return r.lockUpdate(jobID, workerID, `UPDATE public.jobs SET locked_until = $1
		WHERE engine_key = $2 AND status = 'locked' AND locked_by = $3`, until)
}

// Unlock gives a locked job back, to be fetched again.
func (r *ExternalTaskRepository) Unlock(jobID int64) error {
	return r.lockUpdate(jobID, "", `UPDATE public.jobs SET status = 'pending', locked_by = NULL, locked_until = NULL
		WHERE engine_key = $1 AND status = 'locked'`)
}

// LockOwner returns the worker holding a job ("" when it isn't locked),
// and ErrJobNotFound semantics as sql.ErrNoRows.
func (r *ExternalTaskRepository) LockOwner(jobID int64) (string, error) {
	var owner sql.NullString
	err := r.db.Get(&owner, `SELECT CASE WHEN status = 'locked' THEN locked_by END FROM public.jobs WHERE engine_key = $1`, jobID)
	return owner.String, err
}

func (r *ExternalTaskRepository) lockUpdate(jobID int64, workerID, query string, until ...time.Time) error {
	var res sql.Result
	var err error
	if len(until) > 0 {
		res, err = r.db.Exec(query, until[0], jobID, workerID)
	} else {
		res, err = r.db.Exec(query, jobID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrJobNotLockedBy
	}
	return nil
}
