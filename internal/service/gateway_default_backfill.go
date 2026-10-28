package service

import (
	"encoding/json"
	"fmt"

	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jmoiron/sqlx"
)

// BackfillGatewayDefaults fills Node.DefaultFlow into process graphs stored
// before gateways read the BPMN "default" attribute. Without it such a
// graph treats its default flow as an unconditioned flow, which the
// exclusive gateway now takes whenever it comes first in document order.
// Only DefaultFlow is copied from a fresh parse of the stored XML; the rest
// of the stored graph is kept as deployed. Idempotent; returns the number
// of definitions updated.
func BackfillGatewayDefaults(db *sqlx.DB) (int, error) {
	var defs []struct {
		ID          string `db:"id"`
		ProcessKey  string `db:"process_key"`
		BpmnXML     string `db:"bpmn_xml"`
		ParsedGraph []byte `db:"parsed_graph"`
	}
	if err := db.Select(&defs, `
		SELECT id, process_key, bpmn_xml, parsed_graph
		FROM public.process_definitions
		WHERE parsed_graph IS NOT NULL
		  AND bpmn_xml LIKE '%default=%'
		  AND parsed_graph::text NOT LIKE '%"defaultFlow"%'
	`); err != nil {
		return 0, err
	}

	updated := 0
	for _, d := range defs {
		fresh, err := engine.BuildGraphForProcess([]byte(d.BpmnXML), d.ProcessKey)
		if err != nil {
			return updated, fmt.Errorf("definition %s: %w", d.ID, err)
		}
		var stored engine.ProcessGraph
		if err := json.Unmarshal(d.ParsedGraph, &stored); err != nil {
			return updated, fmt.Errorf("definition %s: %w", d.ID, err)
		}
		changed := false
		for id, node := range fresh.Nodes {
			if node.DefaultFlow == "" {
				continue
			}
			if s, ok := stored.Nodes[id]; ok && s.DefaultFlow != node.DefaultFlow {
				s.DefaultFlow = node.DefaultFlow
				changed = true
			}
		}
		if !changed {
			continue
		}
		raw, err := json.Marshal(stored)
		if err != nil {
			return updated, fmt.Errorf("definition %s: %w", d.ID, err)
		}
		if _, err := db.Exec(`UPDATE public.process_definitions SET parsed_graph = $1 WHERE id = $2`, raw, d.ID); err != nil {
			return updated, fmt.Errorf("definition %s: %w", d.ID, err)
		}
		updated++
	}
	return updated, nil
}
