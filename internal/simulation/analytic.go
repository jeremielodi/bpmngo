package simulation

import (
	"fmt"
	"math"
	"sort"

	"github.com/jeremielodi/bpmngo/internal/engine"
)

// Analyze computes long-run expected visits and the resulting time and cost
// per case. Cycles are supported as long as their exit probability is > 0.
func Analyze(s Scenario) (AnalyticResult, error) {
	c, err := compileScenario(s)
	if err != nil {
		return AnalyticResult{}, err
	}
	visits := map[string]float64{c.startID: 1}
	for iteration := 0; iteration < 10000; iteration++ {
		next := map[string]float64{c.startID: 1}
		for id, count := range visits {
			node := c.graph.Nodes[id]
			if normalization := c.joinNormalization[id]; normalization > 0 {
				count /= normalization
			}
			for _, flow := range node.Outgoing {
				next[flow.TargetRef] += count * c.probability(node, flow)
			}
		}
		delta := 0.0
		for id, value := range next {
			delta = math.Max(delta, math.Abs(value-visits[id]))
		}
		visits = next
		if delta < 1e-10 {
			return analyticFromVisits(c, visits)
		}
	}
	return AnalyticResult{}, fmt.Errorf("expected visits did not converge; check for a cycle without an exit")
}

func analyticFromVisits(c *compiledScenario, visits map[string]float64) (AnalyticResult, error) {
	result := AnalyticResult{}
	ids := make([]string, 0, len(c.scenario.Activities))
	for id := range c.scenario.Activities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		parameters := c.scenario.Activities[id]
		processing, err := parameters.ProcessingTime.Expected()
		if err != nil {
			return result, err
		}
		waiting, err := parameters.WaitingTime.Expected()
		if err != nil {
			return result, err
		}
		count := visits[id]
		cost := 0.0
		resourceID := parameters.ResourceID
		if candidates := eligibleResourceIDs(c.resources, parameters); len(candidates) > 0 {
			resourceID = candidates[0]
		}
		if resource, ok := c.resources[resourceID]; ok {
			cost = count * processing / 60 * resource.CostPerHour
		}
		valueAdded := 0.0
		if parameters.ValueAdded {
			valueAdded = count * processing
		}
		element := AnalyticElement{
			ElementID: id, ElementName: c.graph.Nodes[id].Name, ExpectedVisits: count,
			ProcessingMinutes: count * processing, ExplicitWaitMinutes: count * waiting,
			ExpectedCost: cost, ResourceID: resourceID, ValueAddedMinutes: valueAdded,
		}
		result.Elements = append(result.Elements, element)
		result.ProcessingMinutes += element.ProcessingMinutes
		result.WaitingMinutes += element.ExplicitWaitMinutes
		result.ValueAddedMinutes += element.ValueAddedMinutes
		result.CostPerExecution += element.ExpectedCost
	}
	result.CycleTimeMinutes = result.ProcessingMinutes + result.WaitingMinutes
	for splitID, joinID := range c.gatewayPairs {
		split := c.graph.Nodes[splitID]
		durations := make([]float64, len(split.Outgoing))
		for index, flow := range split.Outgoing {
			var err error
			durations[index], err = expectedDurationUntil(c, flow.TargetRef, joinID)
			if err != nil {
				return result, fmt.Errorf("gateway %q branch %q: %w", splitID, flow.ID, err)
			}
		}
		savings := parallelSavings(c, split, durations)
		result.CycleTimeMinutes -= visits[splitID] * savings
	}
	result.CycleTimeMinutes = math.Max(0, result.CycleTimeMinutes)
	if result.CycleTimeMinutes > 0 {
		result.CycleTimeEfficiency = math.Min(1, result.ValueAddedMinutes/result.CycleTimeMinutes)
	}
	return result, nil
}

func expectedDurationUntil(c *compiledScenario, startID, stopID string) (float64, error) {
	visits := map[string]float64{startID: 1}
	for iteration := 0; iteration < 10000; iteration++ {
		next := map[string]float64{startID: 1}
		for id, count := range visits {
			if id == stopID {
				continue
			}
			node := c.graph.Nodes[id]
			if normalization := c.joinNormalization[id]; normalization > 0 {
				count /= normalization
			}
			for _, flow := range node.Outgoing {
				if flow.TargetRef != stopID {
					next[flow.TargetRef] += count * c.probability(node, flow)
				}
			}
		}
		delta := 0.0
		for id, value := range next {
			delta = math.Max(delta, math.Abs(value-visits[id]))
		}
		visits = next
		if delta < 1e-10 {
			total := 0.0
			for id, count := range visits {
				parameters, ok := c.scenario.Activities[id]
				if !ok {
					continue
				}
				processing, _ := parameters.ProcessingTime.Expected()
				waiting, _ := parameters.WaitingTime.Expected()
				total += count * (processing + waiting)
			}
			return total, nil
		}
	}
	return 0, fmt.Errorf("duration did not converge")
}

func parallelSavings(c *compiledScenario, split *engine.Node, durations []float64) float64 {
	if split.Type == engine.ParallelGatewayType {
		sum, maximum := 0.0, 0.0
		for _, duration := range durations {
			sum += duration
			maximum = math.Max(maximum, duration)
		}
		return math.Max(0, sum-maximum)
	}
	probabilities := make([]float64, len(split.Outgoing))
	bestIndex, bestProbability := 0, -1.0
	for index, flow := range split.Outgoing {
		probabilities[index] = c.scenario.Flows[flow.ID].Probability
		if probabilities[index] > bestProbability {
			bestIndex, bestProbability = index, probabilities[index]
		}
	}
	expectedSum, expectedMaximum := 0.0, 0.0
	for mask := 0; mask < 1<<len(probabilities); mask++ {
		probability, maximum, selectedSum := 1.0, 0.0, 0.0
		for index, branchProbability := range probabilities {
			if mask&(1<<index) != 0 {
				probability *= branchProbability
				maximum = math.Max(maximum, durations[index])
				selectedSum += durations[index]
			} else {
				probability *= 1 - branchProbability
			}
		}
		if mask == 0 {
			maximum, selectedSum = durations[bestIndex], durations[bestIndex]
		}
		expectedSum += probability * selectedSum
		expectedMaximum += probability * maximum
	}
	return math.Max(0, expectedSum-expectedMaximum)
}
