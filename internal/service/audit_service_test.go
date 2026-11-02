package service

import (
	"encoding/json"
	"testing"
)

func TestExecutionMovedAuditDataDoesNotDuplicateVariables(t *testing.T) {
	data := executionMovedAuditData(42, "task-a", "task-b")
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["variables"]; exists {
		t.Fatal("EXECUTION_MOVED must not duplicate the process variables")
	}
	if decoded["fromNode"] != "task-a" || decoded["toNode"] != "task-b" {
		t.Fatalf("unexpected movement payload: %#v", decoded)
	}
	if decoded["executionId"] != float64(42) {
		t.Fatalf("unexpected execution id: %#v", decoded["executionId"])
	}
}
