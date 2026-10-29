package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

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

func (s *SimulationService) UpdateScenario(id uuid.UUID, scenario simulation.Scenario, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
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
	return s.repository.UpdateScenario(id, scenario.Name, scenario.BPMNXML, configuration, userID, tenantID)
}

func (s *SimulationService) RestoreScenarioVersion(id uuid.UUID, version int, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	return s.repository.RestoreScenarioVersion(id, version, userID, tenantID)
}

func (s *SimulationService) ExportScenario(id uuid.UUID, tenantID string) (simulation.ScenarioPackage, error) {
	scenario, err := s.LoadScenario(id, tenantID)
	if err != nil {
		return simulation.ScenarioPackage{}, err
	}
	return simulation.ScenarioPackage{Format: "bpmngo-simulation-scenario/v1", ExportedAt: time.Now().UTC(), Scenario: scenario}, nil
}

func (s *SimulationService) ImportScenario(pkg simulation.ScenarioPackage, userID uuid.UUID, tenantID string) (*models.SimulationScenario, error) {
	if pkg.Format != "bpmngo-simulation-scenario/v1" {
		return nil, fmt.Errorf("unsupported scenario package format %q", pkg.Format)
	}
	return s.CreateScenario(pkg.Scenario, userID, tenantID)
}

func (s *SimulationService) CalibrateScenario(id, userID uuid.UUID, tenantID string, request simulation.CalibrationRequest) (simulation.CalibrationResult, error) {
	if request.ProcessDefinitionKey == "" {
		return simulation.CalibrationResult{}, errors.New("processDefinitionKey is required")
	}
	if request.StartedAfter != nil && request.StartedBefore != nil && !request.StartedBefore.After(*request.StartedAfter) {
		return simulation.CalibrationResult{}, errors.New("startedBefore must be after startedAfter")
	}
	if request.MinimumSamples <= 0 {
		request.MinimumSamples = 30
	}
	scenario, err := s.LoadScenario(id, tenantID)
	if err != nil {
		return simulation.CalibrationResult{}, err
	}
	result, err := s.repository.CalibrateFromHistory(request, tenantID)
	if err != nil {
		return result, err
	}
	if !request.Apply {
		return result, nil
	}
	scenario.ArrivalRatePerHour = result.ArrivalRatePerHour
	scenario.ArrivalPattern = result.SuggestedArrivals
	appliedActivities := 0
	for elementID, calibration := range result.Activities {
		parameters, exists := scenario.Activities[elementID]
		if !exists || calibration.Samples < request.MinimumSamples {
			continue
		}
		parameters.ProcessingTime = calibratedDistribution(calibration.MeanProcessingMinutes, calibration.ProcessingStdDevMinutes)
		parameters.WaitingTime = simulation.Distribution{Type: "fixed", Value: calibration.MeanWaitingMinutes}
		scenario.Activities[elementID] = parameters
		appliedActivities++
	}
	if appliedActivities == 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("No scenario activity had at least %d matching historical samples; only the arrival pattern was applied.", request.MinimumSamples))
	}
	row, err := s.UpdateScenario(id, scenario, userID, tenantID)
	if err != nil {
		return result, err
	}
	result.Applied = true
	result.ScenarioVersion = row.CurrentVersion
	return result, nil
}

func calibratedDistribution(mean, standardDeviation float64) simulation.Distribution {
	if mean <= 0 {
		return simulation.Distribution{Type: "fixed", Value: 0}
	}
	// A very wide normal distribution spends too much probability below zero.
	// Cap sigma while retaining observed variability; the sampler already
	// truncates negative values.
	if standardDeviation > 0 {
		if standardDeviation > mean {
			standardDeviation = mean
		}
		return simulation.Distribution{Type: "normal", Mean: mean, StandardDeviation: standardDeviation}
	}
	return simulation.Distribution{Type: "fixed", Value: mean}
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

func (s *SimulationService) EnqueueSimulation(id, userID uuid.UUID, tenantID string) (*models.SimulationRun, error) {
	if _, err := s.LoadScenario(id, tenantID); err != nil {
		return nil, err
	}
	return s.repository.CreateRun(id, userID, "simulation", nil)
}

func (s *SimulationService) EnqueueCapacitySweep(id, userID uuid.UUID, tenantID string, request simulation.CapacitySweepRequest) (*models.SimulationRun, error) {
	scenario, err := s.LoadScenario(id, tenantID)
	if err != nil {
		return nil, err
	}
	if err := simulation.ValidateCapacitySweep(scenario, request); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return s.repository.CreateRun(id, userID, "capacity_sweep", payload)
}

func (s *SimulationService) EnqueueExperiment(id, userID uuid.UUID, tenantID string, request simulation.ExperimentRequest) (*models.SimulationRun, error) {
	scenario, err := s.LoadScenario(id, tenantID)
	if err != nil {
		return nil, err
	}
	if err := simulation.ValidateExperiment(scenario, request); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return s.repository.CreateRun(id, userID, "experiment", payload)
}

func (s *SimulationService) StartWorkers(ctx context.Context, count int) error {
	if count < 1 {
		count = 1
	}
	if err := s.repository.RequeueStaleRuns(); err != nil {
		return err
	}
	workerGroupID := uuid.NewString()
	go s.requeueStaleRuns(ctx)
	for workerID := 0; workerID < count; workerID++ {
		go s.runWorker(ctx, fmt.Sprintf("%s-%d", workerGroupID, workerID))
	}
	return nil
}

func (s *SimulationService) requeueStaleRuns(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.repository.RequeueStaleRuns(); err != nil {
				log.Printf("simulation lease reaper: %v", err)
			}
		}
	}
}

func (s *SimulationService) runWorker(ctx context.Context, workerID string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		run, err := s.repository.ClaimQueuedRun(workerID)
		if errors.Is(err, sql.ErrNoRows) {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		if err != nil {
			log.Printf("simulation worker %s: claim: %v", workerID, err)
			time.Sleep(time.Second)
			continue
		}
		s.executeRunSafely(ctx, run)
	}
}

func (s *SimulationService) executeRunSafely(ctx context.Context, run *models.SimulationRun) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprintf("simulation panic: %v", recovered)
			_ = s.repository.FailRun(run.ID, message)
			log.Printf("simulation run %s: %s", run.ID, message)
		}
	}()
	if err := s.executeRun(ctx, run); err != nil && !errors.Is(err, context.Canceled) {
		_ = s.repository.FailRun(run.ID, err.Error())
	}
}

func (s *SimulationService) executeRun(parent context.Context, run *models.SimulationRun) error {
	scenario, err := s.LoadScenario(run.ScenarioID, "")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				status, statusErr := s.repository.RunStatus(run.ID)
				if statusErr == nil && status == "cancelled" {
					cancel()
					return
				}
				_ = s.repository.HeartbeatRun(run.ID)
			}
		}
	}()
	lastProgress := -1
	reportProgress := func(progress int) {
		if progress == lastProgress || (progress < 100 && progress/2 == lastProgress/2) {
			return
		}
		lastProgress = progress
		status, statusErr := s.repository.RunStatus(run.ID)
		if statusErr == nil && status == "cancelled" {
			cancel()
			return
		}
		_ = s.repository.UpdateRunProgress(run.ID, progress)
	}
	var payload []byte
	var detectedIssues []simulation.Issue
	switch run.RunType {
	case "", "simulation":
		result, runErr := simulation.SimulateWithContext(ctx, scenario, reportProgress)
		if runErr != nil {
			return runErr
		}
		detectedIssues = result.Issues
		payload, err = json.Marshal(result)
	case "capacity_sweep":
		if run.Request == nil {
			return fmt.Errorf("capacity sweep request is missing")
		}
		var request simulation.CapacitySweepRequest
		if err = json.Unmarshal(*run.Request, &request); err != nil {
			return fmt.Errorf("decode capacity sweep: %w", err)
		}
		result, runErr := simulation.SweepCapacity(ctx, scenario, request, reportProgress)
		if runErr != nil {
			return runErr
		}
		payload, err = json.Marshal(result)
	case "experiment":
		if run.Request == nil {
			return fmt.Errorf("experiment request is missing")
		}
		var request simulation.ExperimentRequest
		if err = json.Unmarshal(*run.Request, &request); err != nil {
			return fmt.Errorf("decode experiment: %w", err)
		}
		result, runErr := simulation.RunExperiment(ctx, scenario, request, reportProgress)
		if runErr != nil {
			return runErr
		}
		payload, err = json.Marshal(result)
	default:
		return fmt.Errorf("unsupported simulation run type %q", run.RunType)
	}
	if err != nil {
		return err
	}
	completed, err := s.repository.CompleteRun(run.ID, payload)
	if err != nil || !completed {
		return err
	}
	if run.RunType == "simulation" || run.RunType == "" {
		return s.repository.ReplaceDetectedIssues(run.ScenarioID, run.ID, detectedIssues)
	}
	return nil
}

func scenarioConfiguration(scenario simulation.Scenario) (json.RawMessage, error) {
	copy := scenario
	copy.Name = ""
	copy.BPMNXML = ""
	payload, err := json.Marshal(copy)
	return payload, err
}
