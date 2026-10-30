package simulation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jeremielodi/bpmngo/internal/engine"
)

type compiledScenario struct {
	scenario          Scenario
	graph             *engine.ProcessGraph
	startID           string
	resources         map[string]Resource
	calendars         map[string]*calendarPlan
	gatewayPairs      map[string]string
	joinNormalization map[string]float64
	boundaries        map[string][]*engine.Node
}

func compileScenario(s Scenario) (*compiledScenario, error) {
	s = cloneScenario(s)
	graph, entryPrefix, err := buildProjectGraph(&s)
	if err != nil {
		return nil, fmt.Errorf("build BPMN graph: %w", err)
	}
	starts := make([]string, 0, 1)
	for id, node := range graph.Nodes {
		if node.ParentScope == "" && (entryPrefix == "" || strings.HasPrefix(id, entryPrefix)) {
			switch node.Type {
			case engine.StartEventType, engine.MessageStartEventType, engine.SignalStartEventType, engine.StartTimerEventType:
				starts = append(starts, id)
			}
		}
	}
	if len(starts) != 1 {
		return nil, fmt.Errorf("simulation requires one process start event, got %d", len(starts))
	}
	resources := make(map[string]Resource, len(s.Resources))
	for _, resource := range s.Resources {
		if resource.ID == "" || resource.Capacity < 1 || resource.CostPerHour < 0 {
			return nil, fmt.Errorf("invalid resource %#v", resource)
		}
		if _, duplicate := resources[resource.ID]; duplicate {
			return nil, fmt.Errorf("duplicate resource %q", resource.ID)
		}
		seenSkills := map[string]bool{}
		for _, skill := range resource.Skills {
			if skill == "" || seenSkills[skill] {
				return nil, fmt.Errorf("resource %q has an empty or duplicate skill", resource.ID)
			}
			seenSkills[skill] = true
		}
		resources[resource.ID] = resource
	}
	calendars, err := compileCalendars(s, resources)
	if err != nil {
		return nil, err
	}
	for id, activity := range s.Activities {
		if activity.FixedCost < 0 {
			return nil, fmt.Errorf("activity %q fixed cost must be non-negative", id)
		}
		if graph.Nodes[id] == nil {
			return nil, fmt.Errorf("activity parameters reference unknown element %q", id)
		}
		for _, resourceID := range append([]string{activity.ResourceID}, activity.ResourceIDs...) {
			if resourceID == "" {
				continue
			}
			if _, ok := resources[resourceID]; !ok {
				return nil, fmt.Errorf("activity %q references unknown resource %q", id, resourceID)
			}
		}
		switch activity.RoutingRule {
		case "", "fixed", "least_queue", "round_robin":
		default:
			return nil, fmt.Errorf("activity %q has unsupported routing rule %q", id, activity.RoutingRule)
		}
		if len(activity.RequiredSkills) > 0 && len(eligibleResourceIDs(resources, activity)) == 0 {
			return nil, fmt.Errorf("activity %q has no resource matching skills %v", id, activity.RequiredSkills)
		}
		if _, err := activity.ProcessingTime.Expected(); err != nil {
			return nil, fmt.Errorf("activity %q processing time: %w", id, err)
		}
		if _, err := activity.WaitingTime.Expected(); err != nil {
			return nil, fmt.Errorf("activity %q waiting time: %w", id, err)
		}
	}
	boundaries, err := prepareSimulationGraph(graph, &s)
	if err != nil {
		return nil, err
	}
	pairs, err := pairStructuredGateways(graph)
	if err != nil {
		return nil, err
	}
	joinNormalization := make(map[string]float64, len(pairs))
	for _, node := range graph.Nodes {
		if node.AttachedToRef != "" {
			continue
		}
		if len(node.Outgoing) <= 1 {
			continue
		}
		if node.Type == engine.ParallelGatewayType {
			joinNormalization[pairs[node.ID]] = float64(len(node.Outgoing))
			continue
		}
		if node.Type == engine.InclusiveGatewayType {
			if len(node.Outgoing) > 16 {
				return nil, fmt.Errorf("inclusive gateway %q has %d branches; the simulation limit is 16", node.ID, len(node.Outgoing))
			}
			noneProbability := 1.0
			totalSelected := 0.0
			for _, flow := range node.Outgoing {
				parameter, ok := s.Flows[flow.ID]
				if !ok || parameter.Probability < 0 || parameter.Probability > 1 {
					return nil, fmt.Errorf("inclusive flow %q from %q needs a probability between 0 and 1", flow.ID, node.ID)
				}
				noneProbability *= 1 - parameter.Probability
				totalSelected += parameter.Probability
			}
			if totalSelected == 0 {
				return nil, fmt.Errorf("inclusive gateway %q must select at least one possible flow", node.ID)
			}
			// When no independent branch fires, the most likely branch is the
			// deterministic fallback. This guarantees BPMN's at-least-one rule.
			joinNormalization[pairs[node.ID]] = totalSelected + noneProbability
			continue
		}
		total := 0.0
		for _, flow := range node.Outgoing {
			parameter, ok := s.Flows[flow.ID]
			if !ok || parameter.Probability < 0 {
				return nil, fmt.Errorf("outgoing flow %q from %q needs a non-negative simulation probability", flow.ID, node.ID)
			}
			total += parameter.Probability
		}
		if total < 0.999999 || total > 1.000001 {
			return nil, fmt.Errorf("outgoing probabilities from %q sum to %.6f, want 1", node.ID, total)
		}
	}
	sort.Strings(starts)
	return &compiledScenario{scenario: s, graph: graph, startID: starts[0], resources: resources, calendars: calendars, gatewayPairs: pairs, joinNormalization: joinNormalization, boundaries: boundaries}, nil
}

// prepareSimulationGraph turns embedded subprocess scopes into ordinary graph
// paths and indexes interrupting boundary alternatives. Call activities remain
// atomic activities: their measured/estimated duration represents the called
// process and can be calibrated like any other BPMN activity.
func prepareSimulationGraph(graph *engine.ProcessGraph, scenario *Scenario) (map[string][]*engine.Node, error) {
	boundaries := map[string][]*engine.Node{}
	for _, node := range graph.Nodes {
		if node.AttachedToRef != "" && len(node.Outgoing) > 0 {
			if !node.CancelActivity {
				return nil, fmt.Errorf("non-interrupting boundary event %q is not yet supported by simulation", node.ID)
			}
			probability := 0.0
			for _, flow := range node.Outgoing {
				parameter, ok := scenario.Flows[flow.ID]
				if !ok || parameter.Probability < 0 || parameter.Probability > 1 {
					return nil, fmt.Errorf("boundary flow %q from %q needs a probability between 0 and 1", flow.ID, node.ID)
				}
				probability += parameter.Probability
			}
			if probability > 1.000001 {
				return nil, fmt.Errorf("boundary probabilities from %q sum to %.6f, want at most 1", node.ID, probability)
			}
			boundaries[node.AttachedToRef] = append(boundaries[node.AttachedToRef], node)
		}
	}
	for attachedTo, events := range boundaries {
		if attached := graph.Nodes[attachedTo]; attached != nil {
			ordinary := attached.Outgoing[:0]
			for _, flow := range attached.Outgoing {
				if !flow.IsBoundary {
					ordinary = append(ordinary, flow)
				}
			}
			attached.Outgoing = ordinary
		}
		total := 0.0
		for _, event := range events {
			for _, flow := range event.Outgoing {
				total += scenario.Flows[flow.ID].Probability
			}
		}
		if total > 1.000001 {
			return nil, fmt.Errorf("boundary probabilities attached to %q sum to %.6f, want at most 1", attachedTo, total)
		}
		sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	}
	for _, node := range graph.Nodes {
		if node.Type != engine.SubProcessType {
			continue
		}
		if node.StartNodeID == "" {
			return nil, fmt.Errorf("sub-process %q has no start event", node.ID)
		}
		outer := append([]engine.Flow(nil), node.Outgoing...)
		node.Outgoing = []engine.Flow{{ID: "simulation-enter-" + node.ID, SourceRef: node.ID, TargetRef: node.StartNodeID}}
		foundEnd := false
		for _, inner := range graph.Nodes {
			if inner.ParentScope != node.ID || !inner.IsEndEvent() {
				continue
			}
			foundEnd = true
			inner.Outgoing = make([]engine.Flow, 0, len(outer))
			for _, flow := range outer {
				inner.Outgoing = append(inner.Outgoing, engine.Flow{ID: "simulation-exit-" + node.ID + "-" + flow.ID, SourceRef: inner.ID, TargetRef: flow.TargetRef})
			}
		}
		if !foundEnd {
			return nil, fmt.Errorf("sub-process %q has no end event", node.ID)
		}
		delete(scenario.Activities, node.ID)
	}
	return boundaries, nil
}

func eligibleResourceIDs(resources map[string]Resource, activity ActivityParameters) []string {
	candidates := make([]string, 0, len(activity.ResourceIDs)+1)
	seen := map[string]bool{}
	if activity.ResourceID != "" {
		candidates = append(candidates, activity.ResourceID)
		seen[activity.ResourceID] = true
	}
	for _, id := range activity.ResourceIDs {
		if id != "" && !seen[id] {
			candidates = append(candidates, id)
			seen[id] = true
		}
	}
	if len(candidates) == 0 && len(activity.RequiredSkills) > 0 {
		for id := range resources {
			candidates = append(candidates, id)
		}
		sort.Strings(candidates)
	}
	result := candidates[:0]
	for _, id := range candidates {
		resource, ok := resources[id]
		if ok && resourceHasSkills(resource, activity.RequiredSkills) {
			result = append(result, id)
		}
	}
	return result
}

func resourceHasSkills(resource Resource, required []string) bool {
	for _, requirement := range required {
		found := false
		for _, skill := range resource.Skills {
			if skill == requirement {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (c *compiledScenario) probability(node *engine.Node, flow engine.Flow) float64 {
	if node.Type == engine.ParallelGatewayType && len(node.Outgoing) > 1 {
		return 1
	}
	if node.Type == engine.InclusiveGatewayType && len(node.Outgoing) > 1 {
		probability := c.scenario.Flows[flow.ID].Probability
		noneProbability := 1.0
		bestFlow := node.Outgoing[0].ID
		bestProbability := -1.0
		for _, outgoing := range node.Outgoing {
			p := c.scenario.Flows[outgoing.ID].Probability
			noneProbability *= 1 - p
			if p > bestProbability {
				bestProbability, bestFlow = p, outgoing.ID
			}
		}
		if flow.ID == bestFlow {
			probability += noneProbability
		}
		return probability
	}
	if len(node.Outgoing) == 1 {
		return 1
	}
	return c.scenario.Flows[flow.ID].Probability
}

func pairStructuredGateways(graph *engine.ProcessGraph) (map[string]string, error) {
	pairs := map[string]string{}
	usedJoins := map[string]string{}
	for _, split := range graph.Nodes {
		if len(split.Outgoing) <= 1 || (split.Type != engine.ParallelGatewayType && split.Type != engine.InclusiveGatewayType) {
			continue
		}
		bestID, bestDistance := "", int(^uint(0)>>1)
		for _, candidate := range graph.Nodes {
			if candidate.ID == split.ID || candidate.Type != split.Type || len(candidate.Incoming) <= 1 {
				continue
			}
			totalDistance := 0
			valid := true
			for _, flow := range split.Outgoing {
				distance, ok := shortestDistance(graph, flow.TargetRef, candidate.ID)
				if !ok || !allPathsReach(graph, flow.TargetRef, candidate.ID, map[string]bool{}) {
					valid = false
					break
				}
				totalDistance += distance
			}
			if valid && totalDistance < bestDistance {
				bestID, bestDistance = candidate.ID, totalDistance
			}
		}
		if bestID == "" {
			return nil, fmt.Errorf("%s split %q needs a structured matching join reachable from every branch", split.Type, split.ID)
		}
		if previousSplit := usedJoins[bestID]; previousSplit != "" {
			return nil, fmt.Errorf("gateway join %q is shared by splits %q and %q; simulation requires structured one-to-one pairs", bestID, previousSplit, split.ID)
		}
		pairs[split.ID] = bestID
		usedJoins[bestID] = split.ID
	}
	return pairs, nil
}

func shortestDistance(graph *engine.ProcessGraph, startID, targetID string) (int, bool) {
	type step struct {
		id       string
		distance int
	}
	queue := []step{{startID, 0}}
	seen := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.id == targetID {
			return current.distance, true
		}
		if seen[current.id] {
			continue
		}
		seen[current.id] = true
		if node := graph.Nodes[current.id]; node != nil {
			for _, flow := range node.Outgoing {
				queue = append(queue, step{flow.TargetRef, current.distance + 1})
			}
		}
	}
	return 0, false
}

// allPathsReach rejects unstructured forks where a selected branch can end or
// escape without synchronizing. Cycles are allowed when they still have an
// eventual structured exit; revisiting the active path is treated as neutral.
func allPathsReach(graph *engine.ProcessGraph, nodeID, targetID string, visiting map[string]bool) bool {
	if nodeID == targetID {
		return true
	}
	if visiting[nodeID] {
		return true
	}
	node := graph.Nodes[nodeID]
	if node == nil || len(node.Outgoing) == 0 || node.IsEndEvent() {
		return false
	}
	visiting[nodeID] = true
	defer delete(visiting, nodeID)
	for _, flow := range node.Outgoing {
		if !allPathsReach(graph, flow.TargetRef, targetID, visiting) {
			return false
		}
	}
	return true
}
