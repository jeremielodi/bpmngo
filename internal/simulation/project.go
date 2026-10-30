package simulation

import (
	"fmt"
	"strings"

	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/parser"
)

func ModelElementKey(modelID, elementID string) string { return modelID + "::" + elementID }

// ValidateProjectDraft permits unfinished models so users can save their work
// before configuring workload or completing a diagram. Runs validate the full graph.
func ValidateProjectDraft(s Scenario) error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("project name is required")
	}
	seen := map[string]bool{}
	for _, model := range s.Models {
		if model.ID == "" || strings.Contains(model.ID, "::") || seen[model.ID] {
			return fmt.Errorf("model IDs must be unique, non-empty, and must not contain ::")
		}
		seen[model.ID] = true
		if model.BPMNXML != "" {
			defs, err := parser.ParseBPMN([]byte(model.BPMNXML))
			if err != nil || len(defs.Processes) != 1 {
				return fmt.Errorf("model %q must contain exactly one valid BPMN process", model.Name)
			}
		}
	}
	if s.EntryModelID != "" && !seen[s.EntryModelID] {
		return fmt.Errorf("workload entry model %q does not exist", s.EntryModelID)
	}
	return nil
}

func buildProjectGraph(s *Scenario) (*engine.ProcessGraph, string, error) {
	models := s.Models
	if len(models) == 0 {
		if s.BPMNXML == "" {
			return nil, "", fmt.Errorf("add a BPMN model before running the project")
		}
		defs, err := parser.ParseBPMN([]byte(s.BPMNXML))
		if err != nil {
			return nil, "", err
		}
		if len(defs.Processes) != 1 {
			return nil, "", fmt.Errorf("each project model must contain exactly one BPMN process")
		}
		g, err := engine.BuildGraphFromProcess(&defs.Processes[0])
		return g, "", err
	}
	if err := ValidateProjectDraft(*s); err != nil {
		return nil, "", err
	}
	combined := &engine.ProcessGraph{Nodes: map[string]*engine.Node{}}
	entry := s.EntryModelID
	if entry == "" {
		entry = models[0].ID
	}
	for _, model := range models {
		defs, err := parser.ParseBPMN([]byte(model.BPMNXML))
		if err != nil {
			return nil, "", fmt.Errorf("model %q: %w", model.Name, err)
		}
		if len(defs.Processes) != 1 {
			return nil, "", fmt.Errorf("model %q requires one process", model.Name)
		}
		g, err := engine.BuildGraphFromProcess(&defs.Processes[0])
		if err != nil {
			return nil, "", fmt.Errorf("model %q: %w", model.Name, err)
		}
		key := func(id string) string {
			if id == "" {
				return ""
			}
			return ModelElementKey(model.ID, id)
		}
		for _, node := range g.Nodes {
			node.ID = key(node.ID)
			node.ParentScope, node.StartNodeID = key(node.ParentScope), key(node.StartNodeID)
			node.AttachedToRef, node.DefaultFlow = key(node.AttachedToRef), key(node.DefaultFlow)
			for i := range node.Incoming {
				node.Incoming[i] = key(node.Incoming[i])
			}
			for i := range node.Outgoing {
				f := &node.Outgoing[i]
				f.ID, f.SourceRef, f.TargetRef = key(f.ID), key(f.SourceRef), key(f.TargetRef)
			}
			combined.Nodes[node.ID] = node
		}
	}
	seenSources, seenLinks := map[string]bool{}, map[string]bool{}
	for _, link := range s.Transfers {
		sourceID, targetID := ModelElementKey(link.SourceModelID, link.SourceNodeID), ModelElementKey(link.TargetModelID, link.TargetNodeID)
		source, target := combined.Nodes[sourceID], combined.Nodes[targetID]
		if link.ID == "" || seenLinks[link.ID] || seenSources[sourceID] {
			return nil, "", fmt.Errorf("handoffs need unique IDs and one handoff per source element")
		}
		if source == nil || target == nil {
			return nil, "", fmt.Errorf("handoff %q references an unknown model element", link.ID)
		}
		if link.SourceModelID == link.TargetModelID {
			return nil, "", fmt.Errorf("handoff %q must connect different models", link.ID)
		}
		if source.ParentScope != "" || target.ParentScope != "" {
			return nil, "", fmt.Errorf("handoff %q must connect top-level elements", link.ID)
		}
		if source.Type == engine.ParallelGatewayType || source.Type == engine.InclusiveGatewayType || source.Type == engine.SubProcessType {
			return nil, "", fmt.Errorf("handoff %q must leave a task, event, or exclusive gateway", link.ID)
		}
		if link.Cost < 0 {
			return nil, "", fmt.Errorf("handoff %q cost must be non-negative", link.ID)
		}
		if _, err := link.Delay.Expected(); err != nil {
			return nil, "", fmt.Errorf("handoff %q delay: %w", link.ID, err)
		}
		seenLinks[link.ID], seenSources[sourceID] = true, true
		transferID := "@transfer::" + link.ID
		if combined.Nodes[transferID] != nil {
			return nil, "", fmt.Errorf("handoff ID collision")
		}
		// A handoff replaces the source's normal continuation. The transfer is
		// a timed wait node, retaining the case ID and synchronization frames.
		for _, old := range source.Outgoing {
			if dest := combined.Nodes[old.TargetRef]; dest != nil {
				incoming := dest.Incoming[:0]
				for _, in := range dest.Incoming {
					if in != old.ID && in != sourceID {
						incoming = append(incoming, in)
					}
				}
				dest.Incoming = incoming
			}
		}
		if source.IsEndEvent() {
			source.Type = engine.IntermediateThrowEventType
		}
		source.Outgoing = []engine.Flow{{ID: transferID, SourceRef: sourceID, TargetRef: transferID}}
		combined.Nodes[transferID] = &engine.Node{ID: transferID, Name: "Handoff: " + link.ID, Type: engine.TaskType, Incoming: []string{sourceID}, Outgoing: []engine.Flow{{ID: transferID + "::arrival", SourceRef: transferID, TargetRef: targetID}}}
		target.Incoming = append(target.Incoming, transferID)
		if s.Activities == nil {
			s.Activities = map[string]ActivityParameters{}
		}
		s.Activities[transferID] = ActivityParameters{ProcessingTime: Distribution{Type: "fixed"}, WaitingTime: link.Delay, FixedCost: link.Cost}
	}
	return combined, entry + "::", nil
}
