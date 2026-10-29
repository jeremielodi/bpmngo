package repository

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/simulation"
	"github.com/jmoiron/sqlx"
)

type SimulationRepository struct{ db *sqlx.DB }

func NewSimulationRepository(db *sqlx.DB) *SimulationRepository { return &SimulationRepository{db: db} }

func (r *SimulationRepository) CreateScenario(name, bpmnXML string, configuration json.RawMessage, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	var row models.SimulationScenario
	err := r.db.Get(&row, `INSERT INTO simulation_scenarios(name,bpmn_xml,configuration,created_by,tenant_id)
		VALUES($1,$2,$3,$4,$5) RETURNING id,name,bpmn_xml,configuration,created_by,tenant_id,created_at,updated_at`, name, bpmnXML, configuration, nullableUUID(userID), nullableText(tenantID))
	return &row, err
}

func (r *SimulationRepository) UpdateScenario(id uuid.UUID, name, bpmnXML string, configuration json.RawMessage, tenantID string) (*models.SimulationScenario, error) {
	var row models.SimulationScenario
	err := r.db.Get(&row, `UPDATE simulation_scenarios SET name=$2,bpmn_xml=$3,configuration=$4,updated_at=NOW()
		WHERE id=$1 AND ($5='' OR tenant_id=$5) RETURNING id,name,bpmn_xml,configuration,created_by,tenant_id,created_at,updated_at`, id, name, bpmnXML, configuration, tenantID)
	return &row, err
}

func (r *SimulationRepository) GetScenario(id uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	var row models.SimulationScenario
	err := r.db.Get(&row, `SELECT id,name,bpmn_xml,configuration,created_by,tenant_id,created_at,updated_at FROM simulation_scenarios WHERE id=$1 AND ($2='' OR tenant_id=$2)`, id, tenantID)
	return &row, err
}

func (r *SimulationRepository) ListScenarios(tenantID string) ([]models.SimulationScenario, error) {
	rows := []models.SimulationScenario{}
	err := r.db.Select(&rows, `SELECT id,name,bpmn_xml,configuration,created_by,tenant_id,created_at,updated_at FROM simulation_scenarios WHERE ($1='' OR tenant_id=$1) ORDER BY updated_at DESC`, tenantID)
	return rows, err
}

func (r *SimulationRepository) DeleteScenario(id uuid.UUID, tenantID string) error {
	_, err := r.db.Exec(`DELETE FROM simulation_scenarios WHERE id=$1 AND ($2='' OR tenant_id=$2)`, id, tenantID)
	return err
}

func (r *SimulationRepository) CreateRun(scenarioID, userID uuid.UUID) (*models.SimulationRun, error) {
	var row models.SimulationRun
	err := r.db.Get(&row, `INSERT INTO simulation_runs(scenario_id,status,created_by) VALUES($1,'running',$2)
		RETURNING id,scenario_id,status,result,error_message,created_by,started_at,completed_at`, scenarioID, nullableUUID(userID))
	return &row, err
}

func (r *SimulationRepository) CompleteRun(id uuid.UUID, result json.RawMessage) error {
	_, err := r.db.Exec(`UPDATE simulation_runs SET status='completed',result=$2,completed_at=NOW() WHERE id=$1`, id, result)
	return err
}

func (r *SimulationRepository) FailRun(id uuid.UUID, message string) error {
	_, err := r.db.Exec(`UPDATE simulation_runs SET status='failed',error_message=$2,completed_at=NOW() WHERE id=$1`, id, message)
	return err
}

func (r *SimulationRepository) GetRun(id uuid.UUID, tenantID string) (*models.SimulationRun, error) {
	var row models.SimulationRun
	err := r.db.Get(&row, `SELECT r.id,r.scenario_id,r.status,r.result,r.error_message,r.created_by,r.started_at,r.completed_at FROM simulation_runs r JOIN simulation_scenarios s ON s.id=r.scenario_id WHERE r.id=$1 AND ($2='' OR s.tenant_id=$2)`, id, tenantID)
	return &row, err
}

func (r *SimulationRepository) ListRuns(scenarioID uuid.UUID, tenantID string) ([]models.SimulationRun, error) {
	rows := []models.SimulationRun{}
	err := r.db.Select(&rows, `SELECT r.id,r.scenario_id,r.status,r.result,r.error_message,r.created_by,r.started_at,r.completed_at FROM simulation_runs r JOIN simulation_scenarios s ON s.id=r.scenario_id WHERE r.scenario_id=$1 AND ($2='' OR s.tenant_id=$2) ORDER BY r.started_at DESC`, scenarioID, tenantID)
	return rows, err
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
