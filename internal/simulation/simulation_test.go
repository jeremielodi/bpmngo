package simulation

import (
	"context"
	"math"
	"math/rand"
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

const parallelBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test">
  <process id="parallel" isExecutable="true">
    <startEvent id="start"/><parallelGateway id="fork"/>
    <task id="a" name="A"/><task id="b" name="B"/><parallelGateway id="join"/><endEvent id="end"/>
    <sequenceFlow id="s" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="fa" sourceRef="fork" targetRef="a"/><sequenceFlow id="fb" sourceRef="fork" targetRef="b"/>
    <sequenceFlow id="aj" sourceRef="a" targetRef="join"/><sequenceFlow id="bj" sourceRef="b" targetRef="join"/>
    <sequenceFlow id="e" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`

const inclusiveBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test">
  <process id="inclusive" isExecutable="true">
    <startEvent id="start"/><inclusiveGateway id="fork"/>
    <task id="a" name="A"/><task id="b" name="B"/><inclusiveGateway id="join"/><endEvent id="end"/>
    <sequenceFlow id="s" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="fa" sourceRef="fork" targetRef="a"/><sequenceFlow id="fb" sourceRef="fork" targetRef="b"/>
    <sequenceFlow id="aj" sourceRef="a" targetRef="join"/><sequenceFlow id="bj" sourceRef="b" targetRef="join"/>
    <sequenceFlow id="e" sourceRef="join" targetRef="end"/>
  </process>
</definitions>`

const singleTaskBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test">
  <process id="single" isExecutable="true">
    <startEvent id="start"/><task id="work" name="Work"/><endEvent id="end"/>
    <sequenceFlow id="s" sourceRef="start" targetRef="work"/><sequenceFlow id="e" sourceRef="work" targetRef="end"/>
  </process>
</definitions>`

const priorityBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="test">
  <process id="priority" isExecutable="true">
    <startEvent id="start"/><parallelGateway id="fork"/><task id="low" name="Low"/><task id="high" name="High"/><parallelGateway id="join"/><endEvent id="end"/>
    <sequenceFlow id="s" sourceRef="start" targetRef="fork"/>
    <sequenceFlow id="first_low" sourceRef="fork" targetRef="low"/><sequenceFlow id="second_high" sourceRef="fork" targetRef="high"/>
    <sequenceFlow id="lj" sourceRef="low" targetRef="join"/><sequenceFlow id="hj" sourceRef="high" targetRef="join"/>
    <sequenceFlow id="e" sourceRef="join" targetRef="end"/>
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

func TestParallelGatewaySynchronizesOnCriticalBranch(t *testing.T) {
	scenario := Scenario{
		Name: "parallel", BPMNXML: parallelBPMN, ArrivalRatePerHour: 1, MaxArrivals: 1, Replications: 1, Seed: 1,
		Activities: map[string]ActivityParameters{
			"a": {ProcessingTime: Distribution{Type: "fixed", Value: 10}, ValueAdded: true},
			"b": {ProcessingTime: Distribution{Type: "fixed", Value: 20}, ValueAdded: true},
		}, Flows: map[string]FlowParameters{},
	}
	analytic, err := Analyze(scenario)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, analytic.ProcessingMinutes, 30, 0)
	assertNear(t, analytic.CycleTimeMinutes, 20, .0001)
	result, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, result.Metrics.MeanCycleMinutes, 20, 0)
	assertNear(t, result.Metrics.MeanProcessing, 30, 0)
}

func TestInclusiveGatewayWaitsOnlyForSelectedBranches(t *testing.T) {
	scenario := Scenario{
		Name: "inclusive", BPMNXML: inclusiveBPMN, ArrivalRatePerHour: 1, MaxArrivals: 1, Replications: 1, Seed: 1,
		Activities: map[string]ActivityParameters{
			"a": {ProcessingTime: Distribution{Type: "fixed", Value: 10}, ValueAdded: true},
			"b": {ProcessingTime: Distribution{Type: "fixed", Value: 20}, ValueAdded: true},
		}, Flows: map[string]FlowParameters{"fa": {Probability: 1}, "fb": {Probability: 0}},
	}
	analytic, err := Analyze(scenario)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, analytic.CycleTimeMinutes, 10, .0001)
	result, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, result.Metrics.MeanCycleMinutes, 10, 0)
	if result.Metrics.Completed != 1 {
		t.Fatalf("completed = %d, want 1", result.Metrics.Completed)
	}
}

func TestCapacitySweepComparesEveryCapacity(t *testing.T) {
	scenario := testScenario()
	scenario.DurationHours = 24
	scenario.Replications = 2
	result, err := SweepCapacity(context.Background(), scenario, CapacitySweepRequest{
		ResourceID: "l2", MinimumCapacity: 1, MaximumCapacity: 3, Step: 1, TargetMeanCycleMinutes: 10000,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(result.Points))
	}
	if result.RecommendedCapacity != 1 {
		t.Fatalf("recommended = %d, want 1", result.RecommendedCapacity)
	}
	if result.Points[0].Capacity != 1 || result.Points[2].Capacity != 3 {
		t.Fatalf("unexpected capacities: %#v", result.Points)
	}
	if result.Points[0].Utilization <= result.Points[2].Utilization {
		t.Fatalf("utilization should fall with capacity: %#v", result.Points)
	}
}

func TestScheduledAndPiecewiseArrivalPatterns(t *testing.T) {
	scheduled := Scenario{DurationHours: 2, MaxArrivals: 3, ArrivalPattern: &ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{90, 5, 30, 100}}}
	if err := ValidateRunnable(scheduled); err != nil {
		t.Fatal(err)
	}
	arrivals := scheduleArrivals(scheduled, newTestRNG())
	if len(arrivals) != 3 || arrivals[0] != 5 || arrivals[1] != 30 || arrivals[2] != 90 {
		t.Fatalf("scheduled arrivals = %#v", arrivals)
	}
	piecewise := Scenario{DurationHours: 2, ArrivalPattern: &ArrivalPattern{Type: "piecewise_poisson", Segments: []ArrivalSegment{
		{StartMinute: 0, EndMinute: 30, RatePerHour: 10}, {StartMinute: 60, EndMinute: 90, RatePerHour: 10},
	}}}
	if err := ValidateRunnable(piecewise); err != nil {
		t.Fatal(err)
	}
	for _, arrival := range scheduleArrivals(piecewise, newTestRNG()) {
		if !((arrival >= 0 && arrival <= 30) || (arrival >= 60 && arrival <= 90)) {
			t.Fatalf("arrival %.2f falls outside configured segments", arrival)
		}
	}
}

func TestBusinessCalendarPausesWorkOutsideShifts(t *testing.T) {
	scenario := Scenario{
		Name: "calendar", BPMNXML: singleTaskBPMN, MaxArrivals: 1, Replications: 1, Seed: 1,
		ArrivalPattern: &ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{0}},
		Calendars: []BusinessCalendar{{ID: "office", Name: "Office", Timezone: "UTC", Shifts: []CalendarShift{
			{Weekdays: []int{1, 2}, StartMinute: 0, EndMinute: 60, Capacity: 1},
		}}},
		Resources: []Resource{{ID: "people", Name: "People", Capacity: 1, CalendarID: "office"}},
		Activities: map[string]ActivityParameters{"work": {
			ProcessingTime: Distribution{Type: "fixed", Value: 120}, ResourceID: "people", ValueAdded: true,
		}},
		Flows: map[string]FlowParameters{},
	}
	result, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, result.Metrics.MeanCycleMinutes, 1500, 0)
	assertNear(t, result.Metrics.Resources[0].Utilization, 1, .0001)
}

func TestSkillRoutingAndRoundRobin(t *testing.T) {
	scenario := Scenario{
		Name: "skills", BPMNXML: singleTaskBPMN, Replications: 1, Seed: 1,
		ArrivalPattern: &ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{0, 0, 0, 0}}, DurationHours: 1,
		Resources: []Resource{
			{ID: "general", Name: "General", Capacity: 4, Skills: []string{"basic"}},
			{ID: "specialist", Name: "Specialist", Capacity: 4, Skills: []string{"basic", "special"}},
		},
		Activities: map[string]ActivityParameters{"work": {
			ProcessingTime: Distribution{Type: "fixed", Value: 10}, ResourceIDs: []string{"general", "specialist"},
			RequiredSkills: []string{"special"}, RoutingRule: "round_robin", ValueAdded: true,
		}},
		Flows: map[string]FlowParameters{},
	}
	result, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.Resources[0].BusyMinutes != 0 || result.Metrics.Resources[1].BusyMinutes != 40 {
		t.Fatalf("unexpected skill routing: %#v", result.Metrics.Resources)
	}
	scenario.Activities["work"] = ActivityParameters{
		ProcessingTime: Distribution{Type: "fixed", Value: 10}, ResourceIDs: []string{"general", "specialist"}, RoutingRule: "round_robin", ValueAdded: true,
	}
	result, err = Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	assertNear(t, result.Metrics.Resources[0].BusyMinutes, 20, 0)
	assertNear(t, result.Metrics.Resources[1].BusyMinutes, 20, 0)
}

func TestPriorityQueueStartsMostImportantWorkFirst(t *testing.T) {
	scenario := Scenario{
		Name: "priority", BPMNXML: priorityBPMN, Replications: 1, Seed: 1, DurationHours: 2,
		ArrivalPattern: &ArrivalPattern{Type: "scheduled", ScheduledMinutes: []float64{0}},
		Calendars: []BusinessCalendar{{ID: "shift", Timezone: "UTC", Shifts: []CalendarShift{
			{Weekdays: []int{1}, StartMinute: 60, EndMinute: 180, Capacity: 1},
		}}},
		Resources: []Resource{{ID: "team", Name: "Team", Capacity: 1, CalendarID: "shift"}},
		Activities: map[string]ActivityParameters{
			"low":  {ProcessingTime: Distribution{Type: "fixed", Value: 10}, ResourceID: "team", Priority: 0, ValueAdded: true},
			"high": {ProcessingTime: Distribution{Type: "fixed", Value: 10}, ResourceID: "team", Priority: 10, ValueAdded: true},
		}, Flows: map[string]FlowParameters{},
	}
	result, err := Simulate(scenario)
	if err != nil {
		t.Fatal(err)
	}
	queues := map[string]float64{}
	for _, element := range result.Metrics.Elements {
		queues[element.ElementID] = element.MeanQueueMinutes
	}
	assertNear(t, queues["high"], 60, 0)
	assertNear(t, queues["low"], 70, 0)
	assertNear(t, result.Metrics.MeanCycleMinutes, 80, 0)
}

func newTestRNG() *rand.Rand { return rand.New(rand.NewSource(42)) }

func assertNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("got %.8f, want %.8f (tolerance %.8f)", got, want, tolerance)
	}
}
