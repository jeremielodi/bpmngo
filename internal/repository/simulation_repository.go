package repository

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/simulation"
	"github.com/jmoiron/sqlx"
)

type SimulationRepository struct{ db *sqlx.DB }

func NewSimulationRepository(db *sqlx.DB) *SimulationRepository { return &SimulationRepository{db: db} }

func (r *SimulationRepository) CreateScenario(name, bpmnXML string, configuration json.RawMessage, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var row models.SimulationScenario
	err = tx.Get(&row, `INSERT INTO simulation_scenarios(name,bpmn_xml,configuration,created_by,tenant_id)
		VALUES($1,$2,$3,$4,$5) RETURNING id,name,bpmn_xml,configuration,created_by,tenant_id,current_version,created_at,updated_at`, name, bpmnXML, configuration, nullableUUID(userID), nullableText(tenantID))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO simulation_scenario_versions(scenario_id,version,name,bpmn_xml,configuration,created_by)
		VALUES($1,$2,$3,$4,$5,$6)`, row.ID, row.CurrentVersion, row.Name, row.BPMNXML, row.Configuration, nullableUUID(userID)); err != nil {
		return nil, err
	}
	return &row, tx.Commit()
}

func (r *SimulationRepository) UpdateScenario(id uuid.UUID, name, bpmnXML string, configuration json.RawMessage, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var row models.SimulationScenario
	err = tx.Get(&row, `UPDATE simulation_scenarios SET name=$2,bpmn_xml=$3,configuration=$4,current_version=current_version+1,updated_at=NOW()
		WHERE id=$1 AND ($5='' OR tenant_id=$5) RETURNING id,name,bpmn_xml,configuration,created_by,tenant_id,current_version,created_at,updated_at`, id, name, bpmnXML, configuration, tenantID)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO simulation_scenario_versions(scenario_id,version,name,bpmn_xml,configuration,created_by)
		VALUES($1,$2,$3,$4,$5,$6)`, row.ID, row.CurrentVersion, row.Name, row.BPMNXML, row.Configuration, nullableUUID(userID)); err != nil {
		return nil, err
	}
	return &row, tx.Commit()
}

func (r *SimulationRepository) GetScenario(id uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	var row models.SimulationScenario
	err := r.db.Get(&row, `SELECT id,name,bpmn_xml,configuration,created_by,tenant_id,current_version,created_at,updated_at FROM simulation_scenarios WHERE id=$1 AND ($2='' OR tenant_id=$2)`, id, tenantID)
	return &row, err
}

func (r *SimulationRepository) ListScenarios(tenantID string) ([]models.SimulationScenario, error) {
	rows := []models.SimulationScenario{}
	err := r.db.Select(&rows, `SELECT id,name,bpmn_xml,configuration,created_by,tenant_id,current_version,created_at,updated_at FROM simulation_scenarios WHERE ($1='' OR tenant_id=$1) ORDER BY updated_at DESC`, tenantID)
	return rows, err
}

func (r *SimulationRepository) ListScenarioVersions(scenarioID uuid.UUID, tenantID string) ([]models.SimulationScenarioVersion, error) {
	rows := []models.SimulationScenarioVersion{}
	err := r.db.Select(&rows, `SELECT v.id,v.scenario_id,v.version,v.name,v.created_by,v.created_at
		FROM simulation_scenario_versions v JOIN simulation_scenarios s ON s.id=v.scenario_id
		WHERE v.scenario_id=$1 AND ($2='' OR s.tenant_id=$2) ORDER BY v.version DESC`, scenarioID, tenantID)
	return rows, err
}

func (r *SimulationRepository) GetScenarioVersion(scenarioID uuid.UUID, version int, tenantID string) (*models.SimulationScenarioVersion, error) {
	var row models.SimulationScenarioVersion
	err := r.db.Get(&row, `SELECT v.id,v.scenario_id,v.version,v.name,v.bpmn_xml,v.configuration,v.created_by,v.created_at
		FROM simulation_scenario_versions v JOIN simulation_scenarios s ON s.id=v.scenario_id
		WHERE v.scenario_id=$1 AND v.version=$2 AND ($3='' OR s.tenant_id=$3)`, scenarioID, version, tenantID)
	return &row, err
}

func (r *SimulationRepository) RestoreScenarioVersion(scenarioID uuid.UUID, version int, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var source models.SimulationScenarioVersion
	if err = tx.Get(&source, `SELECT v.id,v.scenario_id,v.version,v.name,v.bpmn_xml,v.configuration,v.created_by,v.created_at
		FROM simulation_scenario_versions v JOIN simulation_scenarios s ON s.id=v.scenario_id
		WHERE v.scenario_id=$1 AND v.version=$2 AND ($3='' OR s.tenant_id=$3) FOR UPDATE OF s`, scenarioID, version, tenantID); err != nil {
		return nil, err
	}
	var row models.SimulationScenario
	if err = tx.Get(&row, `UPDATE simulation_scenarios SET name=$2,bpmn_xml=$3,configuration=$4,current_version=current_version+1,updated_at=NOW()
		WHERE id=$1 RETURNING id,name,bpmn_xml,configuration,created_by,tenant_id,current_version,created_at,updated_at`, scenarioID, source.Name, source.BPMNXML, source.Configuration); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO simulation_scenario_versions(scenario_id,version,name,bpmn_xml,configuration,created_by)
		VALUES($1,$2,$3,$4,$5,$6)`, row.ID, row.CurrentVersion, row.Name, row.BPMNXML, row.Configuration, nullableUUID(userID)); err != nil {
		return nil, err
	}
	return &row, tx.Commit()
}

func (r *SimulationRepository) DeleteScenario(id uuid.UUID, tenantID string) error {
	_, err := r.db.Exec(`DELETE FROM simulation_scenarios WHERE id=$1 AND ($2='' OR tenant_id=$2)`, id, tenantID)
	return err
}

// CalibrateFromHistory derives arrival and activity distributions from the
// archived runtime data without mutating a simulation scenario.
func (r *SimulationRepository) CalibrateFromHistory(request simulation.CalibrationRequest, tenantID string) (simulation.CalibrationResult, error) {
	result := simulation.CalibrationResult{
		ProcessDefinitionKey: request.ProcessDefinitionKey,
		Activities:           map[string]simulation.ActivityCalibration{},
	}
	var stats struct {
		Samples int        `db:"samples"`
		First   *time.Time `db:"first_started_at"`
		Last    *time.Time `db:"last_started_at"`
	}
	if err := r.db.Get(&stats, `SELECT COUNT(*) AS samples, MIN(started_at) AS first_started_at, MAX(started_at) AS last_started_at
		FROM historic_process_instances
		WHERE process_definition_key=$1 AND ($2='' OR COALESCE(tenant_id,'')=$2)
		AND ($3::timestamptz IS NULL OR started_at >= $3) AND ($4::timestamptz IS NULL OR started_at < $4)`,
		request.ProcessDefinitionKey, tenantID, request.StartedAfter, request.StartedBefore); err != nil {
		return result, err
	}
	result.InstanceSamples = stats.Samples
	result.ObservationStart = stats.First
	result.ObservationEnd = stats.Last
	if stats.Samples == 0 || stats.First == nil || stats.Last == nil {
		return result, fmt.Errorf("no historical process instances match process definition key %q", request.ProcessDefinitionKey)
	}
	observedHours := stats.Last.Sub(*stats.First).Hours()
	if request.StartedAfter != nil && request.StartedBefore != nil {
		observedHours = request.StartedBefore.Sub(*request.StartedAfter).Hours()
	}
	if observedHours <= 0 {
		observedHours = 1
	}
	result.ArrivalRatePerHour = float64(stats.Samples) / observedHours
	result.SuggestedArrivals = &simulation.ArrivalPattern{Type: "poisson", RatePerHour: result.ArrivalRatePerHour}

	if observedHours >= 24 {
		type hourlyRate struct {
			Hour int     `db:"hour_of_day"`
			Rate float64 `db:"rate_per_hour"`
		}
		hourly := []hourlyRate{}
		err := r.db.Select(&hourly, `WITH bounds AS (
			SELECT date_trunc('hour', MIN(started_at)) AS first_hour, date_trunc('hour', MAX(started_at)) AS last_hour
			FROM historic_process_instances WHERE process_definition_key=$1 AND ($2='' OR COALESCE(tenant_id,'')=$2)
			AND ($3::timestamptz IS NULL OR started_at >= $3) AND ($4::timestamptz IS NULL OR started_at < $4)
		), hours AS (
			SELECT generate_series(first_hour,last_hour,INTERVAL '1 hour') AS bucket FROM bounds
		), counts AS (
			SELECT date_trunc('hour',started_at) AS bucket, COUNT(*)::float8 AS arrivals
			FROM historic_process_instances WHERE process_definition_key=$1 AND ($2='' OR COALESCE(tenant_id,'')=$2)
			AND ($3::timestamptz IS NULL OR started_at >= $3) AND ($4::timestamptz IS NULL OR started_at < $4)
			GROUP BY 1
		)
		SELECT EXTRACT(HOUR FROM h.bucket)::int AS hour_of_day, AVG(COALESCE(c.arrivals,0))::float8 AS rate_per_hour
		FROM hours h LEFT JOIN counts c USING(bucket) GROUP BY 1 ORDER BY 1`,
			request.ProcessDefinitionKey, tenantID, request.StartedAfter, request.StartedBefore)
		if err != nil {
			return result, err
		}
		segments := make([]simulation.ArrivalSegment, 0, 24)
		rates := make(map[int]float64, len(hourly))
		for _, row := range hourly {
			rates[row.Hour] = row.Rate
		}
		for hour := 0; hour < 24; hour++ {
			segments = append(segments, simulation.ArrivalSegment{StartMinute: float64(hour * 60), EndMinute: float64((hour + 1) * 60), RatePerHour: rates[hour]})
		}
		result.SuggestedArrivals = &simulation.ArrivalPattern{Type: "piecewise_poisson", Segments: segments, RepeatMinutes: 1440}
	} else {
		result.Warnings = append(result.Warnings, "Less than 24 hours of history are available; a stationary Poisson arrival rate was estimated.")
	}

	type activityRow struct {
		ElementID string  `db:"element_id"`
		Name      string  `db:"element_name"`
		Samples   int     `db:"samples"`
		Mean      float64 `db:"mean_minutes"`
		StdDev    float64 `db:"stddev_minutes"`
		Median    float64 `db:"median_minutes"`
		P95       float64 `db:"p95_minutes"`
		Waiting   float64 `db:"waiting_minutes"`
	}
	activities := []activityRow{}
	err := r.db.Select(&activities, `WITH timeline AS (
		SELECT hai.element_id,hai.element_name,hai.occurred_at,
			LEAD(hai.occurred_at) OVER(PARTITION BY hai.process_instance_id,hai.execution_key ORDER BY hai.occurred_at,hai.id) AS next_at
		FROM historic_activity_instances hai
		JOIN historic_process_instances hpi ON hpi.id=hai.process_instance_id
		WHERE hpi.process_definition_key=$1 AND ($2='' OR COALESCE(hpi.tenant_id,'')=$2)
		AND ($3::timestamptz IS NULL OR hpi.started_at >= $3) AND ($4::timestamptz IS NULL OR hpi.started_at < $4)
		AND hai.action IN ('EXECUTION_MOVED','EXECUTION_COMPLETED') AND hai.execution_key IS NOT NULL
	), durations AS (
		SELECT element_id,element_name,EXTRACT(EPOCH FROM (next_at-occurred_at))/60.0 AS minutes
		FROM timeline WHERE element_id IS NOT NULL AND next_at > occurred_at
	)
	SELECT element_id,MAX(COALESCE(element_name,'')) AS element_name,COUNT(*)::int AS samples,
		AVG(minutes)::float8 AS mean_minutes,COALESCE(STDDEV_SAMP(minutes),0)::float8 AS stddev_minutes,
		PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY minutes)::float8 AS median_minutes,
		PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY minutes)::float8 AS p95_minutes
	FROM durations GROUP BY element_id`, request.ProcessDefinitionKey, tenantID, request.StartedAfter, request.StartedBefore)
	if err != nil {
		return result, err
	}
	for _, row := range activities {
		result.Activities[row.ElementID] = simulation.ActivityCalibration{ElementID: row.ElementID, ElementName: row.Name, Samples: row.Samples,
			MeanProcessingMinutes: row.Mean, ProcessingStdDevMinutes: row.StdDev, MedianTotalMinutes: row.Median, P95TotalMinutes: row.P95, Method: "transition_elapsed"}
	}

	tasks := []activityRow{}
	err = r.db.Select(&tasks, `WITH durations AS (
		SELECT ht.task_definition_key AS element_id,ht.task_name AS element_name,
			EXTRACT(EPOCH FROM (ht.completed_at-COALESCE(ht.claimed_at,ht.created_at)))/60.0 AS processing_minutes,
			EXTRACT(EPOCH FROM (COALESCE(ht.claimed_at,ht.created_at)-ht.created_at))/60.0 AS waiting_minutes,
			EXTRACT(EPOCH FROM (ht.completed_at-ht.created_at))/60.0 AS total_minutes
		FROM historic_tasks ht JOIN historic_process_instances hpi ON hpi.id=ht.process_instance_id
		WHERE hpi.process_definition_key=$1 AND ($2='' OR COALESCE(hpi.tenant_id,'')=$2)
		AND ($3::timestamptz IS NULL OR hpi.started_at >= $3) AND ($4::timestamptz IS NULL OR hpi.started_at < $4)
		AND ht.completed_at IS NOT NULL AND ht.completed_at >= ht.created_at AND ht.task_definition_key IS NOT NULL
	)
	SELECT element_id,MAX(COALESCE(element_name,'')) AS element_name,COUNT(*)::int AS samples,
		AVG(processing_minutes)::float8 AS mean_minutes,COALESCE(STDDEV_SAMP(processing_minutes),0)::float8 AS stddev_minutes,
		PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY total_minutes)::float8 AS median_minutes,
		PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY total_minutes)::float8 AS p95_minutes,
		AVG(waiting_minutes)::float8 AS waiting_minutes
	FROM durations GROUP BY element_id`, request.ProcessDefinitionKey, tenantID, request.StartedAfter, request.StartedBefore)
	if err != nil {
		return result, err
	}
	for _, row := range tasks {
		calibration := simulation.ActivityCalibration{ElementID: row.ElementID, ElementName: row.Name, Samples: row.Samples,
			MeanProcessingMinutes: row.Mean, ProcessingStdDevMinutes: row.StdDev, MeanWaitingMinutes: row.Waiting,
			MedianTotalMinutes: row.Median, P95TotalMinutes: row.P95, Method: "historic_task"}
		result.Activities[row.ElementID] = calibration
	}
	result.Warnings = append(result.Warnings, "Transition-based durations can include queue or external waiting time; completed historic tasks use separate claim and processing timestamps when available.")
	return result, nil
}

func (r *SimulationRepository) CreateRun(scenarioID, userID uuid.UUID, runType string, request json.RawMessage) (*models.SimulationRun, error) {
	var row models.SimulationRun
	err := r.db.Get(&row, `INSERT INTO simulation_runs(scenario_id,status,created_by,run_type,request)
		SELECT id,'queued',$2,$3,COALESCE($4::jsonb,'{}'::jsonb) || jsonb_build_object('scenarioVersion',current_version,'scenarioSnapshot',configuration || jsonb_build_object('name',name,'bpmnXml',bpmn_xml))
		FROM simulation_scenarios WHERE id=$1
		RETURNING id,scenario_id,status,run_type,request,progress,result,error_message,created_by,started_at,completed_at`, scenarioID, nullableUUID(userID), runType, nullableJSON(request))
	return &row, err
}

func (r *SimulationRepository) RequeueStaleRuns() error {
	_, err := r.db.Exec(`UPDATE simulation_runs SET status='queued',progress=0,error_message=NULL,worker_id=NULL,heartbeat_at=NULL
		WHERE status='running' AND completed_at IS NULL AND (heartbeat_at IS NULL OR heartbeat_at < NOW()-INTERVAL '30 seconds')`)
	return err
}

func (r *SimulationRepository) ClaimQueuedRun(workerID string) (*models.SimulationRun, error) {
	var row models.SimulationRun
	err := r.db.Get(&row, `WITH candidate AS (
		SELECT id FROM simulation_runs WHERE status='queued' ORDER BY started_at,id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE simulation_runs r SET status='running',progress=0,started_at=NOW(),worker_id=$1,heartbeat_at=NOW()
	FROM candidate WHERE r.id=candidate.id
	RETURNING r.id,r.scenario_id,r.status,r.run_type,r.request,r.progress,r.result,r.error_message,r.created_by,r.started_at,r.completed_at`, workerID)
	return &row, err
}

func (r *SimulationRepository) UpdateRunProgress(id uuid.UUID, progress int) error {
	_, err := r.db.Exec(`UPDATE simulation_runs SET progress=$2,heartbeat_at=NOW() WHERE id=$1 AND status='running'`, id, progress)
	return err
}

func (r *SimulationRepository) HeartbeatRun(id uuid.UUID) error {
	_, err := r.db.Exec(`UPDATE simulation_runs SET heartbeat_at=NOW() WHERE id=$1 AND status='running'`, id)
	return err
}

func (r *SimulationRepository) RunStatus(id uuid.UUID) (string, error) {
	var status string
	err := r.db.Get(&status, `SELECT status FROM simulation_runs WHERE id=$1`, id)
	return status, err
}

func (r *SimulationRepository) CompleteRun(id uuid.UUID, result json.RawMessage) (bool, error) {
	queryResult, err := r.db.Exec(`UPDATE simulation_runs SET status='completed',progress=100,result=$2,completed_at=NOW() WHERE id=$1 AND status='running'`, id, result)
	if err != nil {
		return false, err
	}
	rows, err := queryResult.RowsAffected()
	return rows == 1, err
}

func (r *SimulationRepository) FailRun(id uuid.UUID, message string) error {
	_, err := r.db.Exec(`UPDATE simulation_runs SET status='failed',error_message=$2,completed_at=NOW() WHERE id=$1 AND status IN ('queued','running')`, id, message)
	return err
}

func (r *SimulationRepository) GetRun(id uuid.UUID, tenantID string) (*models.SimulationRun, error) {
	var row models.SimulationRun
	err := r.db.Get(&row, `SELECT r.id,r.scenario_id,r.status,r.run_type,r.request,r.progress,r.result,r.error_message,r.created_by,r.started_at,r.completed_at FROM simulation_runs r JOIN simulation_scenarios s ON s.id=r.scenario_id WHERE r.id=$1 AND ($2='' OR s.tenant_id=$2)`, id, tenantID)
	return &row, err
}

func (r *SimulationRepository) ListRuns(scenarioID uuid.UUID, tenantID string) ([]models.SimulationRun, error) {
	rows := []models.SimulationRun{}
	err := r.db.Select(&rows, `SELECT r.id,r.scenario_id,r.status,r.run_type,r.request,r.progress,r.result,r.error_message,r.created_by,r.started_at,r.completed_at FROM simulation_runs r JOIN simulation_scenarios s ON s.id=r.scenario_id WHERE r.scenario_id=$1 AND ($2='' OR s.tenant_id=$2) ORDER BY r.started_at DESC`, scenarioID, tenantID)
	return rows, err
}

func (r *SimulationRepository) CancelRun(id uuid.UUID, tenantID string) (*models.SimulationRun, error) {
	var row models.SimulationRun
	err := r.db.Get(&row, `UPDATE simulation_runs r SET status='cancelled',completed_at=NOW()
		FROM simulation_scenarios s WHERE r.id=$1 AND s.id=r.scenario_id AND ($2='' OR s.tenant_id=$2) AND r.status IN ('queued','running')
		RETURNING r.id,r.scenario_id,r.status,r.run_type,r.request,r.progress,r.result,r.error_message,r.created_by,r.started_at,r.completed_at`, id, tenantID)
	return &row, err
}

func (r *SimulationRepository) ReplaceDetectedIssues(scenarioID, runID uuid.UUID, issues []simulation.Issue) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM simulation_issues WHERE scenario_id=$1 AND source_run_id IS NOT NULL`, scenarioID); err != nil {
		return err
	}
	for _, issue := range issues {
		var elementID any
		if issue.ElementID != "" {
			elementID = issue.ElementID
		}
		if _, err = tx.Exec(`INSERT INTO simulation_issues(scenario_id,source_run_id,code,element_id,title,evidence,category,severity,suggestion)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, scenarioID, runID, issue.Code, elementID, issue.Title, issue.Evidence, issue.Category, issue.Severity, issue.Suggestion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *SimulationRepository) ListIssues(scenarioID uuid.UUID, tenantID string) ([]models.SimulationIssue, error) {
	rows := []models.SimulationIssue{}
	err := r.db.Select(&rows, `SELECT i.id,i.scenario_id,i.source_run_id,i.code,i.element_id,i.title,i.evidence,i.category,i.severity,i.suggestion,i.status,i.created_at,i.updated_at
		FROM simulation_issues i JOIN simulation_scenarios s ON s.id=i.scenario_id WHERE i.scenario_id=$1 AND ($2='' OR s.tenant_id=$2) ORDER BY CASE i.severity WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,i.created_at DESC`, scenarioID, tenantID)
	return rows, err
}

func (r *SimulationRepository) CreateIssue(issue models.SimulationIssue, tenantID string) (*models.SimulationIssue, error) {
	var row models.SimulationIssue
	err := r.db.Get(&row, `INSERT INTO simulation_issues(scenario_id,code,element_id,title,evidence,category,severity,suggestion,status)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9 FROM simulation_scenarios s WHERE s.id=$1 AND ($10='' OR s.tenant_id=$10)
		RETURNING id,scenario_id,source_run_id,code,element_id,title,evidence,category,severity,suggestion,status,created_at,updated_at`,
		issue.ScenarioID, issue.Code, issue.ElementID, issue.Title, issue.Evidence, issue.Category, issue.Severity, issue.Suggestion, issue.Status, tenantID)
	return &row, err
}

func (r *SimulationRepository) UpdateIssue(id uuid.UUID, issue models.SimulationIssue, tenantID string) (*models.SimulationIssue, error) {
	var row models.SimulationIssue
	err := r.db.Get(&row, `UPDATE simulation_issues i SET code=$2,element_id=$3,title=$4,evidence=$5,category=$6,severity=$7,suggestion=$8,status=$9,updated_at=NOW() WHERE i.id=$1 AND EXISTS(SELECT 1 FROM simulation_scenarios s WHERE s.id=i.scenario_id AND ($10='' OR s.tenant_id=$10))
		RETURNING id,scenario_id,source_run_id,code,element_id,title,evidence,category,severity,suggestion,status,created_at,updated_at`,
		id, issue.Code, issue.ElementID, issue.Title, issue.Evidence, issue.Category, issue.Severity, issue.Suggestion, issue.Status, tenantID)
	return &row, err
}

func (r *SimulationRepository) DeleteIssue(id uuid.UUID, tenantID string) error {
	_, err := r.db.Exec(`DELETE FROM simulation_issues i WHERE i.id=$1 AND EXISTS(SELECT 1 FROM simulation_scenarios s WHERE s.id=i.scenario_id AND ($2='' OR s.tenant_id=$2))`, id, tenantID)
	return err
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
