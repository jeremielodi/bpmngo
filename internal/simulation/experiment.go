package simulation

import (
	"context"
	"fmt"
	"math"
	"strings"
)

func ValidateExperiment(s Scenario, request ExperimentRequest) error {
	if len(request.Dimensions) == 0 {
		return fmt.Errorf("at least one experiment dimension is required")
	}
	combinations := 1
	seen := map[string]bool{}
	for _, dimension := range request.Dimensions {
		if dimension.Path == "" || len(dimension.Values) == 0 {
			return fmt.Errorf("every experiment dimension needs a path and values")
		}
		if seen[dimension.Path] {
			return fmt.Errorf("duplicate experiment path %q", dimension.Path)
		}
		seen[dimension.Path] = true
		combinations *= len(dimension.Values)
		if combinations > 256 {
			return fmt.Errorf("an experiment must not exceed 256 combinations")
		}
		for _, value := range dimension.Values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("experiment path %q contains a non-finite value", dimension.Path)
			}
			candidate := cloneScenario(s)
			if err := setScenarioPath(&candidate, dimension.Path, value); err != nil {
				return err
			}
		}
	}
	switch request.Objective {
	case "", "mean_cycle", "p95_cycle", "cost", "throughput":
	default:
		return fmt.Errorf("unsupported experiment objective %q", request.Objective)
	}
	return nil
}

func RunExperiment(ctx context.Context, scenario Scenario, request ExperimentRequest, progress func(int)) (ExperimentResult, error) {
	if err := ValidateExperiment(scenario, request); err != nil {
		return ExperimentResult{}, err
	}
	objective := request.Objective
	if objective == "" {
		objective = "mean_cycle"
	}
	combinations := experimentCombinations(request.Dimensions)
	result := ExperimentResult{ScenarioName: scenario.Name, Objective: objective}
	bestScore := math.Inf(1)
	for index, parameters := range combinations {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		candidate := cloneScenario(scenario)
		point := ExperimentPoint{Parameters: parameters}
		for path, value := range parameters {
			if err := setScenarioPath(&candidate, path, value); err != nil {
				point.Error = err.Error()
				break
			}
		}
		if point.Error == "" {
			simulationResult, err := simulateContext(ctx, candidate, nil)
			if err != nil {
				point.Error = err.Error()
			} else {
				point.MeanCycleMinutes = simulationResult.Metrics.MeanCycleMinutes
				point.P95CycleMinutes = simulationResult.Metrics.P95CycleMinutes
				point.ThroughputPerHour = simulationResult.Metrics.ThroughputPerHour
				point.MeanCost = simulationResult.Metrics.MeanCost
				point.MeetsConstraints = experimentConstraintsMet(point, request)
				if point.MeetsConstraints {
					score := experimentScore(point, objective)
					if score < bestScore {
						bestScore = score
						recommended := len(result.Points)
						result.RecommendedPoint = &recommended
					}
				}
			}
		}
		result.Points = append(result.Points, point)
		if progress != nil {
			progress((index + 1) * 100 / len(combinations))
		}
	}
	return result, nil
}

func experimentConstraintsMet(point ExperimentPoint, request ExperimentRequest) bool {
	return (request.TargetMeanCycleMinutes <= 0 || point.MeanCycleMinutes <= request.TargetMeanCycleMinutes) &&
		(request.TargetP95CycleMinutes <= 0 || point.P95CycleMinutes <= request.TargetP95CycleMinutes) &&
		(request.MaximumMeanCost <= 0 || point.MeanCost <= request.MaximumMeanCost) &&
		(request.MinimumThroughput <= 0 || point.ThroughputPerHour >= request.MinimumThroughput)
}

func experimentScore(point ExperimentPoint, objective string) float64 {
	switch objective {
	case "p95_cycle":
		return point.P95CycleMinutes
	case "cost":
		return point.MeanCost
	case "throughput":
		return -point.ThroughputPerHour
	default:
		return point.MeanCycleMinutes
	}
}

func experimentCombinations(dimensions []ExperimentDimension) []map[string]float64 {
	result := []map[string]float64{{}}
	for _, dimension := range dimensions {
		next := make([]map[string]float64, 0, len(result)*len(dimension.Values))
		for _, existing := range result {
			for _, value := range dimension.Values {
				combination := make(map[string]float64, len(existing)+1)
				for path, prior := range existing {
					combination[path] = prior
				}
				combination[dimension.Path] = value
				next = append(next, combination)
			}
		}
		result = next
	}
	return result
}

func cloneScenario(s Scenario) Scenario {
	clone := s
	if s.ArrivalPattern != nil {
		pattern := *s.ArrivalPattern
		pattern.Segments = append([]ArrivalSegment(nil), s.ArrivalPattern.Segments...)
		pattern.ScheduledMinutes = append([]float64(nil), s.ArrivalPattern.ScheduledMinutes...)
		clone.ArrivalPattern = &pattern
	}
	clone.Resources = append([]Resource(nil), s.Resources...)
	clone.Activities = make(map[string]ActivityParameters, len(s.Activities))
	for id, activity := range s.Activities {
		activity.ResourceIDs = append([]string(nil), activity.ResourceIDs...)
		activity.RequiredSkills = append([]string(nil), activity.RequiredSkills...)
		clone.Activities[id] = activity
	}
	clone.Flows = make(map[string]FlowParameters, len(s.Flows))
	for id, flow := range s.Flows {
		clone.Flows[id] = flow
	}
	return clone
}

func setScenarioPath(s *Scenario, path string, value float64) error {
	parts := strings.Split(path, ".")
	switch {
	case path == "arrivalRatePerHour":
		s.ArrivalRatePerHour = value
	case path == "durationHours":
		s.DurationHours = value
	case path == "maxArrivals":
		s.MaxArrivals = int(math.Round(value))
	case path == "arrivalPattern.ratePerHour" && s.ArrivalPattern != nil:
		s.ArrivalPattern.RatePerHour = value
	case path == "arrivalPattern.batchSize" && s.ArrivalPattern != nil:
		s.ArrivalPattern.BatchSize = int(math.Round(value))
	case len(parts) == 3 && parts[0] == "resource":
		index := -1
		for i := range s.Resources {
			if s.Resources[i].ID == parts[1] {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("experiment path %q references unknown resource", path)
		}
		switch parts[2] {
		case "capacity":
			s.Resources[index].Capacity = int(math.Round(value))
		case "costPerHour":
			s.Resources[index].CostPerHour = value
		default:
			return fmt.Errorf("unsupported experiment path %q", path)
		}
	case len(parts) == 3 && parts[0] == "flow" && parts[2] == "probability":
		flow, ok := s.Flows[parts[1]]
		if !ok {
			return fmt.Errorf("experiment path %q references unknown flow", path)
		}
		flow.Probability = value
		s.Flows[parts[1]] = flow
	case len(parts) == 3 && parts[0] == "activity" && parts[2] == "priority":
		activity, ok := s.Activities[parts[1]]
		if !ok {
			return fmt.Errorf("experiment path %q references unknown activity", path)
		}
		activity.Priority = int(math.Round(value))
		s.Activities[parts[1]] = activity
	case len(parts) == 4 && parts[0] == "activity" && (parts[2] == "processingTime" || parts[2] == "waitingTime"):
		activity, ok := s.Activities[parts[1]]
		if !ok {
			return fmt.Errorf("experiment path %q references unknown activity", path)
		}
		distribution := &activity.ProcessingTime
		if parts[2] == "waitingTime" {
			distribution = &activity.WaitingTime
		}
		if err := setDistributionValue(distribution, parts[3], value); err != nil {
			return fmt.Errorf("experiment path %q: %w", path, err)
		}
		s.Activities[parts[1]] = activity
	default:
		return fmt.Errorf("unsupported experiment path %q", path)
	}
	return nil
}

func setDistributionValue(distribution *Distribution, field string, value float64) error {
	switch field {
	case "value":
		distribution.Value = value
	case "mean":
		distribution.Mean = value
	case "standardDeviation":
		distribution.StandardDeviation = value
	case "min":
		distribution.Min = value
	case "max":
		distribution.Max = value
	case "mode":
		distribution.Mode = value
	default:
		return fmt.Errorf("unsupported distribution field %q", field)
	}
	return nil
}
