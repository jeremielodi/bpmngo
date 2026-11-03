package common

import (
	"testing"

	"github.com/jeremielodi/bpmngo/internal/engine"
)

func TestJobRetries(t *testing.T) {
	vars := map[string]interface{}{"tries": 7.0}
	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", DefaultJobRetries, false},
		{"5", 5, false},
		{"0", 0, false},
		{"=tries", 7, false},
		{"=2 + 3", 5, false},
		{"-1", 0, true},
		{"many", 0, true},
		{"=1.5", 0, true},
	}
	for _, c := range cases {
		got, err := JobRetries(&engine.Node{JobRetries: c.raw}, vars)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("retries %q: got %d, %v; want %d (error %v)", c.raw, got, err, c.want, c.wantErr)
		}
	}
}

// The model's retries reach the graph (zeebe:taskDefinition retries="5").
func TestJobRetries_FromModel(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/><bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="t"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="work" retries="5"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:sequenceFlow id="f1" sourceRef="t" targetRef="e"/><bpmn:endEvent id="e"/>
  </bpmn:process></bpmn:definitions>`), "p")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := JobRetries(g.Nodes["t"], nil); err != nil || got != 5 {
		t.Fatalf("got %d, %v; want 5", got, err)
	}
}
