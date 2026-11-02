// repository/audit_repository.go
package repository

import (
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jmoiron/sqlx"
)

type AuditRepository struct {
	db *sqlx.DB
}

func NewAuditRepository(db *sqlx.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// CreateAuditLog creates a single audit log entry: queued for the next
// batch when the audit writer runs (see audit_writer.go), written now
// otherwise. Either way it doesn't wait for the write to reach the disk
// (synchronous_commit off): on a crash the last few milliseconds of audit
// rows may be lost, never engine state, which is written durably by its
// own transactions. Waiting for the disk on every audit row was the
// largest cost of running an instance — COMMIT dominated the database
// time under load.
func (r *AuditRepository) CreateAuditLog(log *models.AuditLog) error {
	if log.ID == uuid.Nil {
		log.ID = uuid.New()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}
	if w := currentAuditWriter(); w != nil {
		w.enqueue(log)
		return nil
	}
	return r.insertNow(log)
}

// insertNow writes one audit row in its own transaction.
func (r *AuditRepository) insertNow(log *models.AuditLog) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL synchronous_commit TO OFF`); err != nil {
		return err
	}
	if err := r.CreateAuditLogTx(tx, log); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateAuditLogTx creates a single audit log entry within a transaction
func (r *AuditRepository) CreateAuditLogTx(tx *sqlx.Tx, log *models.AuditLog) error {
	var instanceKey *int64
	if log.ProcessInstanceID != nil {
		key, err := processInstanceEngineKey(tx, *log.ProcessInstanceID)
		if err != nil {
			return err
		}
		instanceKey = &key
	}
	query := `
		INSERT INTO public.audit_logs 
		(id, process_instance_key, task_key, user_id, action, old_data, new_data, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := tx.Exec(query,
		log.ID,
		instanceKey,
		log.TaskID,
		log.UserID,
		string(log.Action),
		log.OldData,
		log.NewData,
		log.CreatedAt,
	)
	return err
}

// GetProcessAuditLogs retrieves all audit logs for a process instance
func (r *AuditRepository) GetProcessAuditLogs(processInstanceID uuid.UUID) ([]models.AuditLog, error) {
	FlushAuditLogs() // rows still queued for their batch
	return r.GetProcessAuditLogsFlushed(processInstanceID)
}

// GetProcessAuditLogsFlushed reads audit rows without forcing the global
// writer to flush. It is used by the background archiver, which flushes once
// for the whole selected batch instead of once per process instance.
func (r *AuditRepository) GetProcessAuditLogsFlushed(processInstanceID uuid.UUID) ([]models.AuditLog, error) {
	var logs []models.AuditLog

	query := `
		SELECT a.id, pi.id AS process_instance_id, a.task_key, a.user_id, a.action, a.old_data, a.new_data, a.created_at
		FROM public.audit_logs a
		JOIN public.process_instances pi ON pi.engine_key = a.process_instance_key
		WHERE pi.id = $1
		ORDER BY created_at ASC
	`

	err := r.db.Select(&logs, query, processInstanceID)
	return logs, err
}

// GetTaskAuditLogs retrieves all audit logs for a task
func (r *AuditRepository) GetTaskAuditLogs(taskID int64) ([]models.AuditLog, error) {
	FlushAuditLogs() // rows still queued for their batch
	var logs []models.AuditLog

	query := `
		SELECT a.id, pi.id AS process_instance_id, a.task_key, a.user_id, a.action, a.old_data, a.new_data, a.created_at
		FROM public.audit_logs a
		JOIN public.process_instances pi ON pi.engine_key = a.process_instance_key
		WHERE a.task_key = $1
		ORDER BY created_at ASC
	`

	err := r.db.Select(&logs, query, taskID)
	return logs, err
}

// GetAuditLogsByAction retrieves audit logs by action type
func (r *AuditRepository) GetAuditLogsByAction(action models.AuditAction, limit int) ([]models.AuditLog, error) {
	FlushAuditLogs() // rows still queued for their batch
	var logs []models.AuditLog

	query := `
		SELECT a.id, pi.id AS process_instance_id, a.task_key, a.user_id, a.action, a.old_data, a.new_data, a.created_at
		FROM public.audit_logs a
		LEFT JOIN public.process_instances pi ON pi.engine_key = a.process_instance_key
		WHERE a.action = $1
		ORDER BY a.created_at DESC
		LIMIT $2
	`

	err := r.db.Select(&logs, query, string(action), limit)
	return logs, err
}

// GetAuditLogsByDateRange retrieves audit logs within a date range
func (r *AuditRepository) GetAuditLogsByDateRange(start, end time.Time) ([]models.AuditLog, error) {
	FlushAuditLogs() // rows still queued for their batch
	var logs []models.AuditLog

	query := `
		SELECT a.id, pi.id AS process_instance_id, a.task_key, a.user_id, a.action, a.old_data, a.new_data, a.created_at
		FROM public.audit_logs a
		LEFT JOIN public.process_instances pi ON pi.engine_key = a.process_instance_key
		WHERE a.created_at BETWEEN $1 AND $2
		ORDER BY a.created_at ASC
	`

	err := r.db.Select(&logs, query, start, end)
	return logs, err
}
