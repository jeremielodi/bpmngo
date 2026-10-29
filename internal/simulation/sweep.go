package simulation

import (
	"context"
	"fmt"
)

func ValidateCapacitySweep(s Scenario, request CapacitySweepRequest) error {
	if request.ResourceID == "" || request.MinimumCapacity < 1 || request.MaximumCapacity < request.MinimumCapacity {
		return fmt.Errorf("resourceId and a valid positive capacity range are required")
	}
	if request.Step <= 0 {
		return fmt.Errorf("step must be positive")
	}
	if (request.MaximumCapacity-request.MinimumCapacity)/request.Step+1 > 100 {
		return fmt.Errorf("a capacity sweep must not exceed 100 points")
	}
	found := false
	for _, resource := range s.Resources {
		found = found || resource.ID == request.ResourceID
	}
	if !found {
		return fmt.Errorf("unknown resource %q", request.ResourceID)
	}
	return ValidateRunnable(s)
}

// SweepCapacity compares a capacity range with common random seeds, which
// reduces noise between points and makes their relative differences useful.
func SweepCapacity(ctx context.Context, scenario Scenario, request CapacitySweepRequest, progress func(int)) (CapacitySweepResult, error) {
	if err := ValidateCapacitySweep(scenario, request); err != nil {
		return CapacitySweepResult{}, err
	}
	pointCount := (request.MaximumCapacity-request.MinimumCapacity)/request.Step + 1
	result := CapacitySweepResult{ScenarioName: scenario.Name, ResourceID: request.ResourceID}
	pointIndex := 0
	for capacity := request.MinimumCapacity; capacity <= request.MaximumCapacity; capacity += request.Step {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		candidate := scenario
		candidate.Resources = append([]Resource(nil), scenario.Resources...)
		for index := range candidate.Resources {
			if candidate.Resources[index].ID == request.ResourceID {
				candidate.Resources[index].Capacity = capacity
			}
		}
		simulationResult, err := simulateContext(ctx, candidate, func(replicationProgress int) {
			if progress != nil {
				progress((pointIndex*100 + replicationProgress) / pointCount)
			}
		})
		if err != nil {
			return result, err
		}
		point := CapacitySweepPoint{
			Capacity: capacity, MeanCycleMinutes: simulationResult.Metrics.MeanCycleMinutes,
			P95CycleMinutes: simulationResult.Metrics.P95CycleMinutes, ThroughputPerHour: simulationResult.Metrics.ThroughputPerHour,
			MeanCost: simulationResult.Metrics.MeanCost,
		}
		for _, resource := range simulationResult.Metrics.Resources {
			if resource.ResourceID == request.ResourceID {
				point.Utilization, point.MeanQueueLength = resource.Utilization, resource.MeanQueueLength
				point.MaximumQueueSize = resource.MaximumQueueSize
				break
			}
		}
		point.MeetsTarget = (request.TargetMeanCycleMinutes <= 0 || point.MeanCycleMinutes <= request.TargetMeanCycleMinutes) &&
			(request.TargetP95CycleMinutes <= 0 || point.P95CycleMinutes <= request.TargetP95CycleMinutes)
		if point.MeetsTarget && result.RecommendedCapacity == 0 {
			result.RecommendedCapacity = capacity
		}
		result.Points = append(result.Points, point)
		pointIndex++
	}
	if progress != nil {
		progress(100)
	}
	return result, nil
}
