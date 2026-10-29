package simulation

import (
	"fmt"
	"math"
)

// CalculateStaffing applies the M/M/c Erlang-C queueing model.
func CalculateStaffing(request StaffingRequest) (StaffingResult, error) {
	if request.ArrivalRatePerHour <= 0 || request.ServiceTimeHours <= 0 || request.TargetWaitingHours < 0 {
		return StaffingResult{}, fmt.Errorf("arrival rate and service time must be positive and target wait non-negative")
	}
	if request.MaxServers < 1 {
		request.MaxServers = 100
	}
	offeredLoad := request.ArrivalRatePerHour * request.ServiceTimeHours
	result := StaffingResult{}
	for servers := 1; servers <= request.MaxServers; servers++ {
		point := StaffingPoint{Servers: servers, Utilization: offeredLoad / float64(servers)}
		if point.Utilization < 1 {
			point.Stable = true
			term, sum := 1.0, 1.0
			for n := 1; n < servers; n++ {
				term *= offeredLoad / float64(n)
				sum += term
			}
			term *= offeredLoad / float64(servers)
			waitingTerm := term / (1 - point.Utilization)
			point.ProbabilityOfWait = waitingTerm / (sum + waitingTerm)
			point.MeanWaitingHours = point.ProbabilityOfWait / (float64(servers)/request.ServiceTimeHours - request.ArrivalRatePerHour)
			point.MeanSystemHours = point.MeanWaitingHours + request.ServiceTimeHours
			point.MeetsTarget = point.MeanWaitingHours <= request.TargetWaitingHours+1e-12
			if point.MeetsTarget && result.MinimumServers == 0 {
				result.MinimumServers = servers
			}
		}
		if math.IsInf(point.MeanWaitingHours, 0) {
			point.MeanWaitingHours = 0
		}
		result.Points = append(result.Points, point)
	}
	if result.MinimumServers == 0 {
		return result, fmt.Errorf("no staffing level up to %d meets the target", request.MaxServers)
	}
	return result, nil
}
