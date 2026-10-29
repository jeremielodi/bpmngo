package simulation

import (
	"fmt"
	"sort"

	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/parser"
)

type compiledScenario struct {
	scenario  Scenario
	graph     *engine.ProcessGraph
	startID   string
	resources map[string]Resource
}

func compileScenario(s Scenario) (*compiledScenario, error) {
	if s.BPMNXML == "" {
		return nil, fmt.Errorf("bpmnXml is required")
	}
	definitions, err := parser.ParseBPMN([]byte(s.BPMNXML))
	if err != nil {
		return nil, fmt.Errorf("parse BPMN: %w", err)
	}
	if len(definitions.Processes) != 1 {
		return nil, fmt.Errorf("simulation requires exactly one BPMN process, got %d", len(definitions.Processes))
	}
	graph, err := engine.BuildGraphFromProcess(&definitions.Processes[0])
	if err != nil {
		return nil, fmt.Errorf("build BPMN graph: %w", err)
	}
	starts := make([]string, 0, 1)
	for id, node := range graph.Nodes {
		if node.ParentScope == "" && len(node.Incoming) == 0 {
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
		resources[resource.ID] = resource
	}
	for id, activity := range s.Activities {
		if graph.Nodes[id] == nil {
			return nil, fmt.Errorf("activity parameters reference unknown element %q", id)
		}
		if activity.ResourceID != "" {
			if _, ok := resources[activity.ResourceID]; !ok {
				return nil, fmt.Errorf("activity %q references unknown resource %q", id, activity.ResourceID)
			}
		}
		if _, err := activity.ProcessingTime.Expected(); err != nil {
			return nil, fmt.Errorf("activity %q processing time: %w", id, err)
		}
		if _, err := activity.WaitingTime.Expected(); err != nil {
			return nil, fmt.Errorf("activity %q waiting time: %w", id, err)
		}
	}
	for _, node := range graph.Nodes {
		if node.Type == engine.ParallelGatewayType || node.Type == engine.InclusiveGatewayType {
			return nil, fmt.Errorf("simulation does not yet support multi-token gateway %q (%s)", node.ID, node.Type)
		}
		if len(node.Outgoing) <= 1 {
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
	return &compiledScenario{scenario: s, graph: graph, startID: starts[0], resources: resources}, nil
}

func (c *compiledScenario) probability(node *engine.Node, flow engine.Flow) float64 {
	if len(node.Outgoing) == 1 {
		return 1
	}
	return c.scenario.Flows[flow.ID].Probability
}
