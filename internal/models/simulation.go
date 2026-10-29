package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type SimulationScenario struct {
	ID             uuid.UUID       `db:"id" json:"id"`
	Name           string          `db:"name" json:"name"`
	BPMNXML        string          `db:"bpmn_xml" json:"bpmnXml"`
	Configuration  json.RawMessage `db:"configuration" json:"configuration"`
	CreatedBy      *uuid.UUID      `db:"created_by" json:"createdBy,omitempty"`
	TenantID       *string         `db:"tenant_id" json:"tenantId,omitempty"`
	CurrentVersion int             `db:"current_version" json:"currentVersion"`
	CreatedAt      time.Time       `db:"created_at" json:"createdAt"`
	UpdatedAt      time.Time       `db:"updated_at" json:"updatedAt"`
}

type SimulationScenarioVersion struct {
	ID            uuid.UUID       `db:"id" json:"id"`
	ScenarioID    uuid.UUID       `db:"scenario_id" json:"scenarioId"`
	Version       int             `db:"version" json:"version"`
	Name          string          `db:"name" json:"name"`
	BPMNXML       string          `db:"bpmn_xml" json:"bpmnXml,omitempty"`
	Configuration json.RawMessage `db:"configuration" json:"configuration,omitempty"`
	CreatedBy     *uuid.UUID      `db:"created_by" json:"createdBy,omitempty"`
	CreatedAt     time.Time       `db:"created_at" json:"createdAt"`
}

type SimulationRun struct {
	ID           uuid.UUID        `db:"id" json:"id"`
	ScenarioID   uuid.UUID        `db:"scenario_id" json:"scenarioId"`
	Status       string           `db:"status" json:"status"`
	RunType      string           `db:"run_type" json:"runType"`
	Request      *json.RawMessage `db:"request" json:"request,omitempty"`
	Progress     int              `db:"progress" json:"progress"`
	Result       *json.RawMessage `db:"result" json:"result,omitempty"`
	ErrorMessage *string          `db:"error_message" json:"errorMessage,omitempty"`
	CreatedBy    *uuid.UUID       `db:"created_by" json:"createdBy,omitempty"`
	StartedAt    time.Time        `db:"started_at" json:"startedAt"`
	CompletedAt  *time.Time       `db:"completed_at" json:"completedAt,omitempty"`
}

type SimulationIssue struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	ScenarioID  uuid.UUID  `db:"scenario_id" json:"scenarioId"`
	SourceRunID *uuid.UUID `db:"source_run_id" json:"sourceRunId,omitempty"`
	Code        string     `db:"code" json:"code"`
	ElementID   *string    `db:"element_id" json:"elementId,omitempty"`
	Title       string     `db:"title" json:"title"`
	Evidence    string     `db:"evidence" json:"evidence"`
	Category    string     `db:"category" json:"category"`
	Severity    string     `db:"severity" json:"severity"`
	Suggestion  string     `db:"suggestion" json:"suggestion"`
	Status      string     `db:"status" json:"status"`
	CreatedAt   time.Time  `db:"created_at" json:"createdAt"`
	UpdatedAt   time.Time  `db:"updated_at" json:"updatedAt"`
}
