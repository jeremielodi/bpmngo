package service

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jeremielodi/bpmngo/internal/models"
	"github.com/jeremielodi/bpmngo/internal/repository"
	"github.com/jeremielodi/bpmngo/internal/simulation"
)

type SimulationService struct {
	repository *repository.SimulationRepository
}

func NewSimulationService(repository *repository.SimulationRepository) *SimulationService {
	return &SimulationService{repository: repository}
}

func (s *SimulationService) CreateScenario(scenario simulation.Scenario, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	if err := simulation.ValidateRunnable(scenario); err != nil {
		return nil, err
	}
	if _, err := simulation.Analyze(scenario); err != nil {
		return nil, err
	}
	configuration, err := scenarioConfiguration(scenario)
	if err != nil {
		return nil, err
	}
	return s.repository.CreateScenario(scenario.Name, scenario.BPMNXML, configuration, userID, tenantID)
}

func (s *SimulationService) UpdateScenario(id uuid.UUID, scenario simulation.Scenario, tenantID string) (*models.SimulationScenario, error) {
	if err := simulation.ValidateRunnable(scenario); err != nil {
		return nil, err
	}
	if _, err := simulation.Analyze(scenario); err != nil {
		return nil, err
	}
	configuration, err := scenarioConfiguration(scenario)
	if err != nil {
		return nil, err
	}
	return s.repository.UpdateScenario(id, scenario.Name, scenario.BPMNXML, configuration, tenantID)
}

func (s *SimulationService) LoadScenario(id uuid.UUID, tenantID string) (simulation.Scenario, error) {
	row, err := s.repository.GetScenario(id, tenantID)
	if err != nil {
		return simulation.Scenario{}, err
	}
	var scenario simulation.Scenario
	if err = json.Unmarshal(row.Configuration, &scenario); err != nil {
		return scenario, fmt.Errorf("decode scenario: %w", err)
	}
	scenario.Name = row.Name
	scenario.BPMNXML = row.BPMNXML
	return scenario, nil
}

func (s *SimulationService) Run(id, userID uuid.UUID, tenantID string) (*models.SimulationRun, error) {
	scenario, err := s.LoadScenario(id, tenantID)
	if err != nil {
		return nil, err
	}
	run, err := s.repository.CreateRun(id, userID)
	if err != nil {
		return nil, err
	}
	result, err := simulation.Simulate(scenario)
	if err != nil {
		_ = s.repository.FailRun(run.ID, err.Error())
		return nil, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err = s.repository.CompleteRun(run.ID, payload); err != nil {
		return nil, err
	}
	if err = s.repository.ReplaceDetectedIssues(id, run.ID, result.Issues); err != nil {
		return nil, err
	}
	return s.repository.GetRun(run.ID, tenantID)
}

func scenarioConfiguration(scenario simulation.Scenario) (json.RawMessage, error) {
	copy := scenario
	copy.Name = ""
	copy.BPMNXML = ""
	payload, err := json.Marshal(copy)
	return payload, err
}
