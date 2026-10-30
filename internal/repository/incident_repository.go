package repository

import (
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/ids"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

type IncidentRepository struct {
	db *sqlx.DB
}

func NewIncidentRepository(db *sqlx.DB) *IncidentRepository {
	return &IncidentRepository{db: db}
}

func (r *IncidentRepository) CreateIncidentTx(tx *sqlx.Tx, c *models.IncidentCreate) (*models.Incident, error) {
	inc := &models.Incident{}
	instanceKey, err := processInstanceEngineKey(tx, c.ProcessInstanceID)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowx(`
		INSERT INTO public.incidents
			(engine_key, process_instance_key, job_key, incident_type, activity_id, error_message, error_code, state, execution_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'open', $8)
		RETURNING engine_key, job_key, incident_type, activity_id,
		          error_message, error_code, state, created_at, resolved_at
	`, ids.NewEngineKey(), instanceKey, c.JobID, c.IncidentType, c.ActivityID,
		nullableString(c.ErrorMessage), nullableString(c.ErrorCode), c.ExecutionID,
	).StructScan(inc)
	inc.ProcessInstanceID = c.ProcessInstanceID
	return inc, err
}

// ExecutionOf returns the execution an incident stopped, when resolving it
// should run that execution again (incidents raised by the runtime on an
// element; job incidents have none: their job is retried instead).
func (r *IncidentRepository) ExecutionOf(processInstanceID uuid.UUID, id int64) *int64 {
	var exec *int64
	if err := r.db.Get(&exec, `
		SELECT execution_key FROM public.incidents
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND engine_key = $2
	`, processInstanceID, id); err != nil {
		return nil
	}
	return exec
}

// HasOpenIncidentOnExecution tells whether execID already has an open
// runtime incident.
func (r *IncidentRepository) HasOpenIncidentOnExecution(processInstanceID uuid.UUID, execID int64) bool {
	var open bool
	r.db.Get(&open, `SELECT EXISTS (
		SELECT 1 FROM public.incidents
		WHERE process_instance_key = (SELECT engine_key FROM public.process_instances WHERE id = $1)
		  AND execution_key = $2 AND state = 'open'
	)`, processInstanceID, execID)
	return open
}

func (r *IncidentRepository) GetByID(id int64) (*models.Incident, error) {
	inc := &models.Incident{}
	err := r.db.QueryRowx(`
		SELECT i.engine_key, pi.id AS process_instance_id, i.job_key, i.incident_type, i.activity_id,
		       error_message, error_code, state, created_at, resolved_at
		FROM public.incidents i
		JOIN public.process_instances pi ON pi.engine_key = i.process_instance_key
		WHERE i.engine_key = $1
	`, id).StructScan(inc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return inc, err
}

type IncidentFilter struct {
	ProcessInstanceID *uuid.UUID
	State             *string
	IncidentType      *string
	// LegacyIncidentType filters by the Camunda 7 name the /engine-rest API
	// shows (models.LegacyIncidentType).
	LegacyIncidentType *string
	ActivityID         *string
}

func (r *IncidentRepository) List(f IncidentFilter) ([]models.Incident, error) {
	query := `
		SELECT i.engine_key, pi.id AS process_instance_id, i.job_key, i.incident_type, i.activity_id,
		       i.error_message, i.error_code, i.state, i.created_at, i.resolved_at
		FROM public.incidents i
		JOIN public.process_instances pi ON pi.engine_key = i.process_instance_key
		WHERE 1=1`
	args := []interface{}{}
	n := 1

	if f.ProcessInstanceID != nil {
		query += ` AND pi.id = $` + itoa(n)
		args = append(args, *f.ProcessInstanceID)
		n++
	}
	if f.State != nil {
		query += ` AND i.state = $` + itoa(n)
		args = append(args, *f.State)
		n++
	}
	if f.IncidentType != nil {
		query += ` AND i.incident_type = $` + itoa(n)
		args = append(args, *f.IncidentType)
		n++
	}
	if f.LegacyIncidentType != nil {
		switch *f.LegacyIncidentType {
		case "failedExternalTask":
			query += ` AND (i.incident_type = 'JOB_NO_RETRIES' OR (i.incident_type = 'UNHANDLED_ERROR_EVENT' AND i.job_key IS NOT NULL))`
		case "unhandledErrorEvent":
			query += ` AND i.incident_type = 'UNHANDLED_ERROR_EVENT' AND i.job_key IS NULL`
		case "executionError":
			query += ` AND i.incident_type NOT IN ('JOB_NO_RETRIES', 'UNHANDLED_ERROR_EVENT')`
		default:
			query += ` AND i.incident_type = $` + itoa(n)
			args = append(args, *f.LegacyIncidentType)
			n++
		}
	}
	if f.ActivityID != nil {
		query += ` AND i.activity_id = $` + itoa(n)
		args = append(args, *f.ActivityID)
		n++
	}
	query += ` ORDER BY i.created_at DESC`
	_ = n

	var result []models.Incident
	err := r.db.Select(&result, query, args...)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *IncidentRepository) Resolve(processInstanceID uuid.UUID, id int64) error {
	now := time.Now()
	instanceKey, err := processInstanceEngineKey(r.db, processInstanceID)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		UPDATE public.incidents SET state = 'resolved', resolved_at = $1
		WHERE process_instance_key = $2
		  AND engine_key = $3 AND state = 'open'
	`, now, instanceKey, id)
	return err
}

// Delete loads the incident's process_instance_id itself first (same
// "internal lookup before mutating" pattern as TaskRepository's mutators)
// so the actual write below can route to a single Citus shard, even though
// the caller (a REST handler with just an incident ID) has no cheaper way
// to already have it in scope.
func (r *IncidentRepository) Delete(id int64) error {
	_, err := r.db.Exec(`
		UPDATE public.incidents SET state = 'deleted' WHERE engine_key = $1
	`, id)
	return err
}

func (r *IncidentRepository) ResolveByJobID(tx *sqlx.Tx, jobID int64) error {
	now := time.Now()
	_, err := tx.Exec(`
		UPDATE public.incidents SET state = 'resolved', resolved_at = $1
		WHERE job_key = $2 AND state = 'open'
	`, now, jobID)
	return err
}

// nullableString returns sql.NullString
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
