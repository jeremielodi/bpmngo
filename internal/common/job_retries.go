package common

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jeremielodi/bpmngo/internal/engine"
)

// DefaultJobRetries is a job's retries when its task definition gives none.
const DefaultJobRetries = 3

// StaticJobRetries is the retries of node's jobs when they don't depend on
// variables (empty or a literal number); false for an expression or an
// invalid value, which JobRetries evaluates and reports.
func StaticJobRetries(node *engine.Node) (int, bool) {
	raw := strings.TrimSpace(node.JobRetries)
	if raw == "" {
		return DefaultJobRetries, true
	}
	if strings.HasPrefix(raw, "=") {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= 0
}

// JobRetries is the retries a new job of node starts with, from its
// zeebe:taskDefinition retries: a number, or a FEEL expression evaluated
// against variables, as in Camunda 8.
func JobRetries(node *engine.Node, variables map[string]interface{}) (int, error) {
	if n, ok := StaticJobRetries(node); ok {
		return n, nil
	}
	raw := strings.TrimSpace(node.JobRetries)
	if !strings.HasPrefix(raw, "=") {
		return 0, fmt.Errorf("retries %q is not a non-negative number", raw)
	}
	v, err := EvaluateValue(raw, variables)
	if err != nil {
		return 0, fmt.Errorf("retries %q: %w", raw, err)
	}
	var f float64
	switch x := v.(type) {
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case float64:
		f = x
	case string:
		if f, err = strconv.ParseFloat(x, 64); err != nil {
			return 0, fmt.Errorf("retries %q gave %q, not a number", raw, x)
		}
	default:
		return 0, fmt.Errorf("retries %q gave %v, not a number", raw, v)
	}
	if f < 0 || f != math.Trunc(f) {
		return 0, fmt.Errorf("retries %q gave %v, not a non-negative whole number", raw, f)
	}
	return int(f), nil
}
