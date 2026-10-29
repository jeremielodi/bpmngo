package simulation

import (
	"fmt"
	"math"
	"sort"
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
		if resource, ok := c.resources[parameters.ResourceID]; ok {
			cost = count * processing / 60 * resource.CostPerHour
		}
		valueAdded := 0.0
		if parameters.ValueAdded {
			valueAdded = count * processing
		}
		element := AnalyticElement{
			ElementID: id, ElementName: c.graph.Nodes[id].Name, ExpectedVisits: count,
			ProcessingMinutes: count * processing, ExplicitWaitMinutes: count * waiting,
			ExpectedCost: cost, ResourceID: parameters.ResourceID, ValueAddedMinutes: valueAdded,
		}
		result.Elements = append(result.Elements, element)
		result.ProcessingMinutes += element.ProcessingMinutes
		result.WaitingMinutes += element.ExplicitWaitMinutes
		result.ValueAddedMinutes += element.ValueAddedMinutes
		result.CostPerExecution += element.ExpectedCost
	}
	result.CycleTimeMinutes = result.ProcessingMinutes + result.WaitingMinutes
	if result.CycleTimeMinutes > 0 {
		result.CycleTimeEfficiency = result.ValueAddedMinutes / result.CycleTimeMinutes
	}
	return result, nil
}
