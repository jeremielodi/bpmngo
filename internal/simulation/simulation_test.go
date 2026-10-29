package simulation

import (
	"math"
	"testing"
)

const testBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test">
  <process id="helpdesk" isExecutable="true">
    <startEvent id="start"><outgoing>to_check</outgoing></startEvent>
    <userTask id="check" name="Check request"><incoming>to_check</incoming><outgoing>to_gateway</outgoing></userTask>
    <exclusiveGateway id="known"><incoming>to_gateway</incoming><outgoing>known_flow</outgoing><outgoing>unknown_flow</outgoing></exclusiveGateway>
    <userTask id="send" name="Send known answer"><incoming>known_flow</incoming><outgoing>send_end</outgoing></userTask>
    <userTask id="solve" name="Solve issue"><incoming>unknown_flow</incoming><outgoing>solve_end</outgoing></userTask>
    <endEvent id="end"><incoming>send_end</incoming><incoming>solve_end</incoming></endEvent>
    <sequenceFlow id="to_check" sourceRef="start" targetRef="check"/>
    <sequenceFlow id="to_gateway" sourceRef="check" targetRef="known"/>
    <sequenceFlow id="known_flow" sourceRef="known" targetRef="send"/>
    <sequenceFlow id="unknown_flow" sourceRef="known" targetRef="solve"/>
    <sequenceFlow id="send_end" sourceRef="send" targetRef="end"/>
    <sequenceFlow id="solve_end" sourceRef="solve" targetRef="end"/>
  </process>
</definitions>`

func testScenario() Scenario {
	return Scenario{
		Name: "helpdesk", BPMNXML: testBPMN, ArrivalRatePerHour: 1, DurationHours: 100,
		Replications: 3, Seed: 42,
		Resources: []Resource{{ID: "l1", Name: "Level 1", Capacity: 5, CostPerHour: 40}, {ID: "l2", Name: "Level 2", Capacity: 3, CostPerHour: 60}},
		Activities: map[string]ActivityParameters{
			"check": {ProcessingTime: Distribution{Type: "fixed", Value: 10}, WaitingTime: Distribution{Type: "fixed", Value: 60}, ResourceID: "l1", ValueAdded: true},
			"send":  {ProcessingTime: Distribution{Type: "fixed", Value: 5}, ResourceID: "l1", ValueAdded: true},
			"solve": {ProcessingTime: Distribution{Type: "fixed", Value: 120}, WaitingTime: Distribution{Type: "fixed", Value: 120}, ResourceID: "l2", ValueAdded: true},
		},
		Flows: map[string]FlowParameters{"known_flow": {Probability: .2}, "unknown_flow": {Probability: .8}},
	}
}

func TestAnalyzeExpectedTimeAndCost(t *testing.T) {
	result, err := Analyze(testScenario())
	if err != nil {
		t.Fatal(err)
	}
	wantProcessing := 10.0 + .2*5 + .8*120
	wantWaiting := 60.0 + .8*120
	wantCost := 10.0/60*40 + .2*5.0/60*40 + .8*120.0/60*60
	assertNear(t, result.ProcessingMinutes, wantProcessing, .0001)
	assertNear(t, result.WaitingMinutes, wantWaiting, .0001)
	assertNear(t, result.CostPerExecution, wantCost, .0001)
	assertNear(t, result.CycleTimeEfficiency, wantProcessing/(wantProcessing+wantWaiting), .0001)
}

func TestCalculateStaffingFindsStableMinimum(t *testing.T) {
	result, err := CalculateStaffing(StaffingRequest{ArrivalRatePerHour: 1, ServiceTimeHours: 2, TargetWaitingHours: 2, MaxServers: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.MinimumServers != 3 {
		t.Fatalf("minimum servers = %d, want 3", result.MinimumServers)
	}
	if result.Points[1].Stable {
		t.Fatal("two servers at offered load 2 must not be stable")
	}
}

func TestSimulationIsReproducibleAndCompletesCases(t *testing.T) {
	scenario := testScenario()
	first, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if first.Metrics.Completed == 0 || first.Metrics.Completed != second.Metrics.Completed {
		t.Fatalf("unexpected completions: %d and %d", first.Metrics.Completed, second.Metrics.Completed)
	}
	assertNear(t, first.Metrics.MeanCycleMinutes, second.Metrics.MeanCycleMinutes, 0)
	if len(first.Metrics.Resources) != 2 {
		t.Fatalf("resource metrics = %d, want 2", len(first.Metrics.Resources))
	}
}

func TestEventQueueOrdersByTimeThenInsertion(t *testing.T) {
	queue := eventQueue{}
	queue.push(simEvent{at: 5, seq: 2})
	queue.push(simEvent{at: 1, seq: 3})
	queue.push(simEvent{at: 1, seq: 1})
	for index, want := range []int64{1, 3, 2} {
		if got := queue.pop().seq; got != want {
			t.Fatalf("pop %d sequence = %d, want %d", index, got, want)
		}
	}
}

func assertNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("got %.8f, want %.8f (tolerance %.8f)", got, want, tolerance)
	}
}
