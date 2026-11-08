package simulation

import (
	"context"
	"strings"
	"testing"
)

func teamScenario(xml string, capacity int) Scenario {
	return Scenario{Name: "Shared team", BPMNXML: xml, Replications: 1, Seed: 1,
		ArrivalPattern: &ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{0}},
		Resources:      []Resource{{ID: "team", Name: "Team", Capacity: capacity, CostPerHour: 60}},
		Activities:     map[string]ActivityParameters{},
	}
}

func teamActivity(units int, minutes float64) ActivityParameters {
	return ActivityParameters{ResourceID: "team", ResourceUnits: units, ProcessingTime: Distribution{Type: "fixed", Value: minutes}}
}

func traceTime(t *testing.T, result SimulationResult, kind, element string, caseID int) float64 {
	t.Helper()
	for _, event := range result.Metrics.Trace {
		if event.Kind == kind && event.ElementID == element && event.CaseID == caseID {
			return event.At
		}
	}
	t.Fatalf("missing %s event for %s, case %d", kind, element, caseID)
	return 0
}

func TestSharedPoolSupportsDifferentTeamSizes(t *testing.T) {
	for _, capacity := range []int{4, 3} {
		s := teamScenario(parallelBPMN, capacity)
		s.Activities["a"] = teamActivity(1, 10)
		group := teamActivity(3, 20)
		group.FixedCost = 5
		s.Activities["b"] = group
		r, err := Simulate(s)
		if err != nil {
			t.Fatal(err)
		}
		start, finish := 0.0, 20.0
		if capacity == 3 {
			start, finish = 10, 30
		}
		assertNear(t, traceTime(t, r, "start", "b", 0), start, 0)
		assertNear(t, r.Metrics.MeanCycleMinutes, finish, 0)
		assertNear(t, r.Metrics.MeanCost, 75, 0) // 10 + 3*20 person-minutes, plus one fixed cost.
		assertNear(t, r.Analytic.CostPerExecution, 75, 0)
		assertNear(t, r.Metrics.Resources[0].BusyMinutes, 70, 0)
		if r.Metrics.Resources[0].Utilization > 1 {
			t.Fatal("team overallocated")
		}
		for _, event := range r.Metrics.Trace {
			if event.ElementID == "b" && event.Kind == "start" && event.ResourceUnits != 3 {
				t.Fatal("missing team size in execution log")
			}
		}
	}
}

func TestWholeTeamIsReleasedAndLegacyTasksStillUseOnePerson(t *testing.T) {
	for _, units := range []int{0, 1, 2, 3} {
		s := teamScenario(singleTaskBPMN, 3)
		s.Activities["work"] = teamActivity(units, 10)
		s.ArrivalPattern.ScheduledMinutes = []float64{0, 0, 0}
		r, err := Simulate(s)
		if err != nil {
			t.Fatal(err)
		}
		if r.Metrics.Completed != 3 {
			t.Fatal("team was not released for queued cases")
		}
		start := 0.0
		if units > 1 {
			start = 20
		}
		assertNear(t, traceTime(t, r, "start", "work", 2), start, 0)
		assertNear(t, r.Metrics.MeanCost, float64(s.Activities["work"].requiredUnits())*10, 0)
	}
}

func TestTeamRequirementsValidateAndFilterEligiblePools(t *testing.T) {
	s := teamScenario(singleTaskBPMN, 2)
	s.Activities["work"] = teamActivity(3, 10)
	if _, err := Simulate(s); err == nil || !strings.Contains(err.Error(), "needs 3 people") {
		t.Fatalf("expected capacity validation, got %v", err)
	}
	s.Resources = append(s.Resources, Resource{ID: "larger", Capacity: 3, CostPerHour: 60})
	a := s.Activities["work"]
	a.ResourceIDs, a.RoutingRule = []string{"larger"}, "round_robin"
	s.Activities["work"] = a
	s.ArrivalPattern.ScheduledMinutes = []float64{0, 0}
	r, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range r.Metrics.Trace {
		if event.Kind == "start" && event.ResourceID != "larger" {
			t.Fatalf("selected undersized pool: %+v", event)
		}
	}
	a.ResourceID, a.ResourceIDs = "", nil
	s.Activities["work"] = a
	if _, err := Simulate(s); err == nil {
		t.Fatal("team requirement without a pool was silently ignored")
	}
	a.ResourceUnits = -1
	s.Activities["work"] = a
	if _, err := Simulate(s); err == nil {
		t.Fatal("negative team size accepted")
	}
}

func withTeamCalendar(s Scenario, shifts ...CalendarShift) Scenario {
	s.StartTime = "2026-11-02T00:00:00Z"
	s.Resources[0].CalendarID = "office"
	s.Calendars = []BusinessCalendar{{ID: "office", Timezone: "UTC", Shifts: shifts}}
	return s
}

func TestTeamsWaitAndPauseUntilEnoughPeopleAreOnShift(t *testing.T) {
	s := teamScenario(singleTaskBPMN, 3)
	s.Activities["work"] = teamActivity(3, 10)
	s = withTeamCalendar(s,
		CalendarShift{Weekdays: []int{1}, StartMinute: 0, EndMinute: 5, Capacity: 2},
		CalendarShift{Weekdays: []int{1}, StartMinute: 5, EndMinute: 10, Capacity: 3},
		CalendarShift{Weekdays: []int{1}, StartMinute: 10, EndMinute: 15, Capacity: 2},
		CalendarShift{Weekdays: []int{1}, StartMinute: 15, EndMinute: 1440, Capacity: 3},
	)
	r, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, traceTime(t, r, "start", "work", 0), 5, 0)
	assertNear(t, r.Metrics.MeanCycleMinutes, 20, 0)
	assertNear(t, r.Metrics.MeanCost, 30, 0)
	s.Calendars[0].Shifts = s.Calendars[0].Shifts[:1]
	if _, err := Simulate(s); err == nil || !strings.Contains(err.Error(), "3 people together") {
		t.Fatalf("expected impossible shift error, got %v", err)
	}
}

func TestChangingShiftsDoNotOverbookConcurrentTeams(t *testing.T) {
	s := teamScenario(parallelBPMN, 3)
	s.Activities["a"], s.Activities["b"] = teamActivity(2, 15), teamActivity(1, 10)
	s = withTeamCalendar(s,
		CalendarShift{Weekdays: []int{1}, StartMinute: 0, EndMinute: 5, Capacity: 3},
		CalendarShift{Weekdays: []int{1}, StartMinute: 5, EndMinute: 1440, Capacity: 2},
	)
	r, err := Simulate(s)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, traceTime(t, r, "complete", "a", 0), 15, 0)
	assertNear(t, traceTime(t, r, "complete", "b", 0), 20, 0)
	assertNear(t, r.Metrics.MeanCost, 40, 0)
	assertNear(t, r.Metrics.Resources[0].BusyMinutes, 40, 0)
	assertNear(t, r.Metrics.Resources[0].Utilization, 40.0/45.0, .00001)
}

func TestTeamSizeCanBeVariedInExperiments(t *testing.T) {
	s := teamScenario(singleTaskBPMN, 3)
	s.Activities["work"] = teamActivity(1, 10)
	s.ArrivalPattern.ScheduledMinutes = []float64{0, 0}
	r, err := RunExperiment(context.Background(), s, ExperimentRequest{Dimensions: []ExperimentDimension{{Path: "activity.work.resourceUnits", Values: []float64{1, 2, 3, 4}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Points) != 4 {
		t.Fatal("missing experiment points")
	}
	for i := 0; i < 3; i++ {
		if r.Points[i].Error != "" {
			t.Fatal(r.Points[i].Error)
		}
		assertNear(t, r.Points[i].MeanCost, float64(i+1)*10, 0)
	}
	if r.Points[3].Error == "" {
		t.Fatal("experiment accepted an impossible team size")
	}
}
