package simulation

import (
	"context"
	"encoding/json"
	"testing"
)

func linkedProject() Scenario {
	return Scenario{Name: "Order and fulfillment", EntryModelID: "sales", Models: []ProjectModel{
		{ID: "sales", Name: "Sales", BPMNXML: singleTaskBPMN},
		{ID: "delivery", Name: "Delivery", BPMNXML: singleTaskBPMN},
	}, Transfers: []ModelTransfer{{ID: "shipment", SourceModelID: "sales", SourceNodeID: "end", TargetModelID: "delivery", TargetNodeID: "start", Delay: Distribution{Type: "fixed", Value: 5}, Cost: 7}},
		ArrivalPattern: &ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{0}}, Replications: 2, Seed: 42,
		Activities: map[string]ActivityParameters{
			"sales::work":    {ProcessingTime: Distribution{Type: "fixed", Value: 2}, FixedCost: 3},
			"delivery::work": {ProcessingTime: Distribution{Type: "fixed", Value: 4}, FixedCost: 11},
		}}
}

func TestProjectHandoffPreservesCaseAndRecordsActualPath(t *testing.T) {
	s := linkedProject()
	before, _ := json.Marshal(s)
	r, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Metrics.Completed != 1 {
		t.Fatalf("completed %d", r.Metrics.Completed)
	}
	assertNear(t, r.Metrics.MeanCycleMinutes, 11, 0)
	assertNear(t, r.Metrics.MeanCost, 21, 0)
	assertNear(t, r.Analytic.CostPerExecution, 21, 0)
	assertNear(t, r.Analytic.CycleTimeMinutes, 11, 0)
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("compilation mutated the source project")
	}
	visited := map[string]bool{}
	lastAt := -1.0
	for _, e := range r.Metrics.Trace {
		if e.CaseID != 0 || e.At < lastAt {
			t.Fatalf("invalid case or chronology: %+v", e)
		}
		lastAt = e.At
		visited[e.ElementID] = true
	}
	for _, id := range []string{"sales::work", "@transfer::shipment", "delivery::work", "delivery::end"} {
		if !visited[id] {
			t.Fatalf("missing trace of %s", id)
		}
	}
	if r.ScenarioSnapshot == nil || len(r.ScenarioSnapshot.Models) != 2 {
		t.Fatal("missing project snapshot")
	}
	if len(r.Metrics.FlowCounts) < 5 {
		t.Fatal("missing actual path counts")
	}
}

func TestProjectSharesResourcesAcrossModels(t *testing.T) {
	s := linkedProject()
	s.ArrivalPattern.ScheduledMinutes = []float64{0, 0}
	s.Resources = []Resource{{ID: "team", Capacity: 1, CostPerHour: 60}}
	for id, a := range s.Activities {
		a.ResourceID = "team"
		s.Activities[id] = a
	}
	r, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Metrics.Completed != 2 || r.Metrics.Resources[0].MaximumQueueSize < 1 {
		t.Fatal("shared work was not queued")
	}
	assertNear(t, r.Metrics.MeanCost, 27, 0)
}

func TestProjectDraftAndInvalidTransfers(t *testing.T) {
	if err := ValidateProjectDraft(Scenario{Name: "Empty project"}); err != nil {
		t.Fatal(err)
	}
	s := linkedProject()
	s.Transfers[0].TargetNodeID = "absent"
	if _, err := Simulate(s); err == nil {
		t.Fatal("accepted an invalid handoff")
	}
	s = linkedProject()
	s.Models[1].ID = "sales"
	if err := ValidateProjectDraft(s); err == nil {
		t.Fatal("accepted duplicate model IDs")
	}
}

func TestProjectExperimentsAndCalendarsUseNamespacedActivities(t *testing.T) {
	s := linkedProject()
	s.StartTime = "2026-11-02T08:00:00Z"
	s.Resources = []Resource{{ID: "team", Capacity: 1, CalendarID: "office", Skills: []string{"review"}}}
	s.Calendars = []BusinessCalendar{{ID: "office", Timezone: "UTC", Shifts: []CalendarShift{{Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 540, EndMinute: 1020}}}}
	a := s.Activities["delivery::work"]
	a.RequiredSkills = []string{"review"}
	s.Activities["delivery::work"] = a
	r, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, r.Metrics.MeanCycleMinutes, 64, 0)
	experiment, err := RunExperiment(context.Background(), s, ExperimentRequest{Dimensions: []ExperimentDimension{{Path: "activity.delivery::work.processingTime.value", Values: []float64{4, 8}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(experiment.Points) != 2 || experiment.Points[0].Error != "" || experiment.Points[1].Error != "" {
		t.Fatalf("invalid experiment: %+v", experiment)
	}
	assertNear(t, experiment.Points[1].MeanCycleMinutes, 68, 0)
	sweep, err := SweepCapacity(context.Background(), s, CapacitySweepRequest{ResourceID: "team", MinimumCapacity: 1, MaximumCapacity: 2, Step: 1}, nil)
	if err != nil || len(sweep.Points) != 2 {
		t.Fatalf("project sweep: %v %+v", err, sweep)
	}
}

func TestProjectPreservesExistingBPMNSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, xml string
		durations map[string]float64
		flows     map[string]FlowParameters
		cycle     float64
	}{
		{"subprocess and call activity", subprocessBPMN, map[string]float64{"inside": 7, "called": 3}, nil, 19},
		{"parallel synchronization", parallelBPMN, map[string]float64{"a": 2, "b": 4}, nil, 13},
		{"inclusive synchronization", inclusiveBPMN, map[string]float64{"a": 2, "b": 4}, map[string]FlowParameters{"fa": {Probability: 1}, "fb": {Probability: 1}}, 13},
		{"event gateway", eventGatewayBPMN, map[string]float64{"a": 2, "b": 4}, map[string]FlowParameters{"message_flow": {Probability: 1}, "timer_flow": {Probability: 0}}, 11},
		{"boundary alternative", boundaryBPMN, map[string]float64{"work": 2, "normal": 1, "fallback": 4}, map[string]FlowParameters{"timeout_flow": {Probability: 1}}, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := linkedProject()
			s.Models[0].BPMNXML = tc.xml
			delete(s.Activities, "sales::work")
			for id, duration := range tc.durations {
				s.Activities[ModelElementKey("sales", id)] = ActivityParameters{ProcessingTime: Distribution{Type: "fixed", Value: duration}}
			}
			s.Flows = map[string]FlowParameters{}
			for id, flow := range tc.flows {
				s.Flows[ModelElementKey("sales", id)] = flow
			}
			result, err := Simulate(s)
			if err != nil {
				t.Fatal(err)
			}
			if result.Metrics.Completed != 1 {
				t.Fatalf("completed %d", result.Metrics.Completed)
			}
			assertNear(t, result.Metrics.MeanCycleMinutes, tc.cycle, 0)
		})
	}
}

func TestProjectReplaySamplingPreservesFullPathCounts(t *testing.T) {
	s := linkedProject()
	s.ArrivalPattern.ScheduledMinutes = make([]float64, 60)
	result, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Metrics.TraceTruncated || result.Metrics.Completed != 60 {
		t.Fatal("expected bounded replay with complete aggregate metrics")
	}
	for _, event := range result.Metrics.Trace {
		if event.CaseID >= 50 {
			t.Fatal("replay exceeded its case sample")
		}
	}
	for _, flow := range result.Metrics.FlowCounts {
		if flow.Count != 60 {
			t.Fatalf("flow %s counted %d cases", flow.FlowID, flow.Count)
		}
	}
}
