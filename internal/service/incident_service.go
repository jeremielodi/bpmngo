// internal/service/incident_service.go
package service

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/eventlog"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jmoiron/sqlx"
)

// SetJobRetries resets a failed job to pending with new retries and
// resolves any open incident for it — extracted from what used to be the
// entire inline body of internal/api/incident_controller.go's
// SetJobRetries handler, now shared with the Partition Processor
// (internal/partition/processor.go) for jobs belonging to a partitioned-
// write-path instance.
func SetJobRetries(db *sqlx.DB, jobID int64, retries int) error {
	incidentRepo := repository.NewIncidentRepository(db)

	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var processInstanceID uuid.UUID
	if err := tx.QueryRowx(`
		SELECT pi.id FROM public.jobs j
		JOIN public.process_instances pi ON pi.engine_key = j.process_instance_key
		WHERE j.engine_key = $1
	`, jobID).Scan(&processInstanceID); err != nil {
		return fmt.Errorf("failed to load job: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE public.jobs
		SET status = 'pending', retries = $1, locked_by = NULL, locked_until = NULL, updated_at = NOW()
		WHERE engine_key = $2
	`, retries, jobID); err != nil {
		return fmt.Errorf("failed to update job retries: %w", err)
	}

	// Fetch the open incident's identity (if any) before resolving it, so
	// IncidentResolved can be published after commit — see below.
	var incidentID int64
	hasOpenIncident := tx.QueryRowx(`
		SELECT engine_key FROM public.incidents WHERE job_key = $1 AND state = 'open' LIMIT 1
	`, jobID).Scan(&incidentID) == nil

	if err := incidentRepo.ResolveByJobID(tx, jobID); err != nil {
		return fmt.Errorf("failed to resolve incident: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	if p := eventlog.Global(); p != nil {
		// Published unconditionally, unlike IncidentResolved below, so the
		// Command Gateway's bridge always has a confirmation signal to
		// wait on regardless of whether an incident existed.
		p.JobRetriesSet(processInstanceID.String(), strconv.FormatInt(jobID, 10), int32(retries))
		if hasOpenIncident {
			p.IncidentResolved(processInstanceID.String(), strconv.FormatInt(incidentID, 10), strconv.FormatInt(jobID, 10))
		}
	}

	return nil
}

// ResolveIncident flips an incident's state to resolved — extracted from
// what used to be the entire inline body of internal/zeebegrpc/server.go's
// ResolveIncident RPC, now shared with the Partition Processor. Unlike
// SetJobRetries, this never touches the job's own retries/status (a
// pre-existing asymmetry between the two endpoints, not something this
// extraction changes).
func ResolveIncident(db *sqlx.DB, incidentID int64) error {
	incidentRepo := repository.NewIncidentRepository(db)

	inc, err := incidentRepo.GetByID(incidentID)
	if err != nil {
		return fmt.Errorf("failed to load incident: %w", err)
	}
	if inc == nil {
		return fmt.Errorf("incident %d not found", incidentID)
	}

	if err := incidentRepo.Resolve(inc.ProcessInstanceID, incidentID); err != nil {
		return fmt.Errorf("failed to resolve incident: %w", err)
	}

	if p := eventlog.Global(); p != nil {
		jobID := ""
		if inc.JobID != nil {
			jobID = strconv.FormatInt(*inc.JobID, 10)
		}
		p.IncidentResolved(inc.ProcessInstanceID.String(), strconv.FormatInt(incidentID, 10), jobID)
	}

	return nil
}
