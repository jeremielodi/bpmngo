package simulation

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/jeremielodi/bpmngo/internal/engine"
)

type eventKind int

const (
	eventArrive eventKind = iota
	eventEnter
	eventServiceDone
	eventResourceWake
)

type simEvent struct {
	at         float64
	seq        int64
	kind       eventKind
	caseID     int
	nodeID     string
	resourceID string
	queuedAt   float64
	frames     []syncFrame
}
type eventQueue []simEvent

func eventBefore(left, right simEvent) bool {
	return left.at < right.at || (left.at == right.at && left.seq < right.seq)
}
func (q *eventQueue) push(event simEvent) {
	*q = append(*q, event)
	for child := len(*q) - 1; child > 0; {
		parent := (child - 1) / 2
		if !eventBefore((*q)[child], (*q)[parent]) {
			break
		}
		(*q)[child], (*q)[parent] = (*q)[parent], (*q)[child]
		child = parent
	}
}
func (q *eventQueue) pop() simEvent {
	event := (*q)[0]
	last := (*q)[len(*q)-1]
	*q = (*q)[:len(*q)-1]
	if len(*q) > 0 {
		(*q)[0] = last
		for parent := 0; ; {
			left := parent*2 + 1
			if left >= len(*q) {
				break
			}
			best := left
			right := left + 1
			if right < len(*q) && eventBefore((*q)[right], (*q)[left]) {
				best = right
			}
			if !eventBefore((*q)[best], (*q)[parent]) {
				break
			}
			(*q)[parent], (*q)[best] = (*q)[best], (*q)[parent]
			parent = best
		}
	}
	return event
}

type syncFrame struct {
	joinID  string
	groupID int64
}
type joinState struct{ expected, arrived int }
type caseState struct {
	started, processing, waiting, cost float64
	joins                              map[int64]*joinState
}
type queuedWork struct {
	caseID   int
	nodeID   string
	queuedAt float64
	priority int
	sequence int64
	frames   []syncFrame
}
type resourceState struct {
	resource                                        Resource
	busy                                            int
	busyMinutes, queueArea, capacityMinutes, lastAt float64
	maxQueue                                        int
	queue                                           []queuedWork
	wakeAt                                          float64
}
type elementState struct {
	name           string
	completed      int
	queue, service float64
}

// Simulate runs independent, deterministic-by-seed discrete-event replications.
func Simulate(s Scenario) (SimulationResult, error) {
	return simulateContext(context.Background(), s, nil)
}

func SimulateWithContext(ctx context.Context, s Scenario, progress func(int)) (SimulationResult, error) {
	return simulateContext(ctx, s, progress)
}

func simulateContext(ctx context.Context, s Scenario, progress func(int)) (SimulationResult, error) {
	c, err := compileScenario(s)
	if err != nil {
		return SimulationResult{}, err
	}
	if err := ValidateRunnable(s); err != nil {
		return SimulationResult{}, err
	}
	if s.Replications <= 0 {
		s.Replications = 1
	}
	analytic, err := Analyze(s)
	if err != nil {
		return SimulationResult{}, err
	}
	runs := make([]RunMetrics, 0, s.Replications)
	for i := 0; i < s.Replications; i++ {
		select {
		case <-ctx.Done():
			return SimulationResult{}, ctx.Err()
		default:
		}
		m, e := runOnce(ctx, c, rand.New(rand.NewSource(s.Seed+int64(i))))
		if e != nil {
			return SimulationResult{}, e
		}
		runs = append(runs, m)
		if progress != nil {
			progress((i + 1) * 100 / s.Replications)
		}
	}
	metrics := averageRuns(runs)
	result := SimulationResult{ScenarioName: s.Name, GeneratedAt: time.Now().UTC(), Replications: s.Replications, Metrics: metrics, Analytic: analytic}
	result.CycleTime95 = confidence(runs, func(m RunMetrics) float64 { return m.MeanCycleMinutes })
	result.Cost95 = confidence(runs, func(m RunMetrics) float64 { return m.MeanCost })
	result.Throughput95 = confidence(runs, func(m RunMetrics) float64 { return m.ThroughputPerHour })
	result.Issues = DetectIssues(result)
	result.RecommendedDesign = RecommendRedesign(result.Issues)
	return result, nil
}

// ValidateRunnable checks limits and arrival settings without executing a campaign.
func ValidateRunnable(s Scenario) error {
	if err := validateArrivalPattern(s); err != nil {
		return err
	}
	if s.DurationHours <= 0 && s.MaxArrivals <= 0 && !hasFiniteArrivalPattern(s) {
		return fmt.Errorf("durationHours or maxArrivals must be positive")
	}
	if s.Replications > 100 {
		return fmt.Errorf("replications must not exceed 100")
	}
	if s.MaxArrivals > 1_000_000 || (s.MaxArrivals <= 0 && arrivalEstimate(s) > 1_000_000) {
		return fmt.Errorf("a campaign must not schedule more than 1,000,000 expected arrivals")
	}
	return nil
}

func runOnce(ctx context.Context, c *compiledScenario, rng *rand.Rand) (RunMetrics, error) {
	duration := c.scenario.DurationHours * 60
	q := &eventQueue{}
	var sequence int64
	var nextGroupID int64
	push := func(e simEvent) { sequence++; e.seq = sequence; q.push(e) }
	arrivals := scheduleArrivals(c.scenario, rng)
	for count, arrival := range arrivals {
		if count&1023 == 0 {
			select {
			case <-ctx.Done():
				return RunMetrics{}, ctx.Err()
			default:
			}
		}
		push(simEvent{at: arrival, kind: eventArrive, caseID: count, nodeID: c.startID})
	}
	count := len(arrivals)
	resources := map[string]*resourceState{}
	for id, r := range c.resources {
		resources[id] = &resourceState{resource: r}
	}
	cases := map[int]*caseState{}
	elements := map[string]*elementState{}
	cycles := []float64{}
	finalAt := duration
	updateArea := func(resourceID string, r *resourceState, at float64) {
		r.queueArea += float64(len(r.queue)) * (at - r.lastAt)
		if plan := c.calendars[resourceID]; plan != nil {
			r.capacityMinutes += plan.capacityMinutesBetween(r.lastAt, at)
		} else {
			r.capacityMinutes += (at - r.lastAt) * float64(r.resource.Capacity)
		}
		r.lastAt = at
	}
	capacityAt := func(resourceID string, at float64) int {
		if plan := c.calendars[resourceID]; plan != nil {
			return plan.capacityAt(at)
		}
		return resources[resourceID].resource.Capacity
	}
	var enter func(int, string, float64, []syncFrame) error
	var startWork func(string, *resourceState, queuedWork, float64) error
	var drainQueue func(string, float64) error
	var scheduleWake func(string, float64)
	var dispatch func(int, string, float64, []syncFrame) error
	var release func(string, float64) error
	routingIndexes := map[string]int{}
	var queueSequence int64
	startWork = func(resourceID string, r *resourceState, w queuedWork, at float64) error {
		p := c.scenario.Activities[w.nodeID]
		service, e := p.ProcessingTime.Sample(rng)
		if e != nil {
			return e
		}
		completedAt := at + service
		if plan := c.calendars[resourceID]; plan != nil {
			completedAt, e = plan.completionAt(at, service)
			if e != nil {
				return e
			}
		}
		r.busy++
		r.busyMinutes += service
		st := cases[w.caseID]
		st.waiting += at - w.queuedAt
		st.processing += service
		st.cost += service / 60 * r.resource.CostPerHour
		el := elements[w.nodeID]
		el.queue += at - w.queuedAt
		el.service += service
		push(simEvent{at: completedAt, kind: eventServiceDone, caseID: w.caseID, nodeID: w.nodeID, resourceID: resourceID, frames: w.frames})
		return nil
	}
	scheduleWake = func(resourceID string, at float64) {
		r := resources[resourceID]
		plan := c.calendars[resourceID]
		if plan == nil || len(r.queue) == 0 {
			return
		}
		next, ok := plan.nextChange(at)
		if !ok || (r.wakeAt > at && r.wakeAt <= next) {
			return
		}
		r.wakeAt = next
		push(simEvent{at: next, kind: eventResourceWake, resourceID: resourceID})
	}
	drainQueue = func(resourceID string, at float64) error {
		r := resources[resourceID]
		updateArea(resourceID, r, at)
		capacity := capacityAt(resourceID, at)
		for len(r.queue) > 0 && r.busy < capacity {
			work := r.queue[0]
			r.queue = r.queue[1:]
			if err := startWork(resourceID, r, work, at); err != nil {
				return err
			}
		}
		if len(r.queue) > 0 {
			scheduleWake(resourceID, at)
		}
		return nil
	}
	selectResource := func(nodeID string, p ActivityParameters, at float64) string {
		candidates := eligibleResourceIDs(c.resources, p)
		if len(candidates) == 0 {
			return ""
		}
		if len(candidates) == 1 || p.RoutingRule == "fixed" {
			return candidates[0]
		}
		if p.RoutingRule == "round_robin" {
			index := routingIndexes[nodeID] % len(candidates)
			routingIndexes[nodeID]++
			return candidates[index]
		}
		bestID, bestScore := candidates[0], math.Inf(1)
		for _, resourceID := range candidates {
			r := resources[resourceID]
			capacity := capacityAt(resourceID, at)
			score := float64(r.busy + len(r.queue))
			if capacity > 0 {
				score /= float64(capacity)
			} else if plan := c.calendars[resourceID]; plan != nil {
				next, ok := plan.nextAvailable(at)
				if !ok {
					continue
				}
				score += 1_000_000 + next - at
			}
			if score < bestScore {
				bestID, bestScore = resourceID, score
			}
		}
		return bestID
	}
	dispatch = func(caseID int, nodeID string, at float64, frames []syncFrame) error {
		p := c.scenario.Activities[nodeID]
		resourceID := selectResource(nodeID, p, at)
		if resourceID == "" {
			service, err := p.ProcessingTime.Sample(rng)
			if err != nil {
				return err
			}
			cases[caseID].processing += service
			elements[nodeID].service += service
			push(simEvent{at: at + service, kind: eventServiceDone, caseID: caseID, nodeID: nodeID, frames: frames})
			return nil
		}
		r := resources[resourceID]
		updateArea(resourceID, r, at)
		if plan := c.calendars[resourceID]; plan != nil && plan.capacityAt(at) == 0 {
			if _, ok := plan.nextAvailable(at); !ok {
				return fmt.Errorf("resource %q calendar has no availability within two weeks", resourceID)
			}
		}
		queueSequence++
		r.queue = append(r.queue, queuedWork{caseID: caseID, nodeID: nodeID, queuedAt: at, priority: p.Priority, sequence: queueSequence, frames: frames})
		sort.SliceStable(r.queue, func(i, j int) bool {
			return r.queue[i].priority > r.queue[j].priority || (r.queue[i].priority == r.queue[j].priority && r.queue[i].sequence < r.queue[j].sequence)
		})
		if len(r.queue) > r.maxQueue {
			r.maxQueue = len(r.queue)
		}
		return drainQueue(resourceID, at)
	}
	release = func(resourceID string, at float64) error {
		r := resources[resourceID]
		updateArea(resourceID, r, at)
		r.busy--
		return drainQueue(resourceID, at)
	}
	enter = func(caseID int, nodeID string, at float64, frames []syncFrame) error {
		node := c.graph.Nodes[nodeID]
		if node == nil {
			return fmt.Errorf("unknown node %q", nodeID)
		}
		if len(node.Incoming) > 1 && len(frames) > 0 && frames[len(frames)-1].joinID == nodeID {
			frame := frames[len(frames)-1]
			state := cases[caseID].joins[frame.groupID]
			if state == nil {
				return fmt.Errorf("missing synchronization group %d at gateway %q", frame.groupID, nodeID)
			}
			state.arrived++
			if state.arrived < state.expected {
				return nil
			}
			delete(cases[caseID].joins, frame.groupID)
			frames = frames[:len(frames)-1]
		}
		if (node.IsEndEvent() && node.ParentScope == "") || len(node.Outgoing) == 0 {
			if len(frames) > 0 {
				return fmt.Errorf("token reached end before structured join %q", frames[len(frames)-1].joinID)
			}
			st := cases[caseID]
			cycles = append(cycles, at-st.started)
			delete(cases, caseID)
			return nil
		}
		p, activity := c.scenario.Activities[nodeID]
		if activity {
			wait, e := p.WaitingTime.Sample(rng)
			if e != nil {
				return e
			}
			if wait > 0 {
				cases[caseID].waiting += wait
				elements[nodeID].queue += wait
				push(simEvent{at: at + wait, kind: eventEnter, caseID: caseID, nodeID: nodeID, queuedAt: -1, frames: frames})
				return nil
			}
			return dispatch(caseID, nodeID, at, frames)
		}
		return route(c, node, caseID, at, rng, frames, push, &nextGroupID, cases)
	}
	var err error
	processedEvents := 0
	for len(*q) > 0 {
		processedEvents++
		if processedEvents&1023 == 0 {
			select {
			case <-ctx.Done():
				return RunMetrics{}, ctx.Err()
			default:
			}
		}
		e := q.pop()
		finalAt = math.Max(finalAt, e.at)
		switch e.kind {
		case eventArrive:
			cases[e.caseID] = &caseState{started: e.at, joins: map[int64]*joinState{}}
			push(simEvent{at: e.at, kind: eventEnter, caseID: e.caseID, nodeID: e.nodeID})
		case eventEnter:
			if _, ok := elements[e.nodeID]; !ok {
				n := c.graph.Nodes[e.nodeID]
				elements[e.nodeID] = &elementState{name: n.Name}
			}
			if e.queuedAt < 0 { // explicit wait elapsed: enter service without waiting again
				err = dispatch(e.caseID, e.nodeID, e.at, e.frames)
			} else {
				err = enter(e.caseID, e.nodeID, e.at, e.frames)
			}
		case eventServiceDone:
			elements[e.nodeID].completed++
			if e.resourceID != "" {
				err = release(e.resourceID, e.at)
			}
			if err == nil {
				err = routeAfterActivity(c, c.graph.Nodes[e.nodeID], e.caseID, e.at, rng, e.frames, push, &nextGroupID, cases)
			}
		case eventResourceWake:
			resources[e.resourceID].wakeAt = 0
			err = drainQueue(e.resourceID, e.at)
		}
		if err != nil {
			return RunMetrics{}, err
		}
	}
	m := RunMetrics{Arrived: count, Completed: len(cycles)}
	sort.Float64s(cycles)
	if len(cycles) > 0 {
		for _, v := range cycles {
			m.MeanCycleMinutes += v
		}
		m.MeanCycleMinutes /= float64(len(cycles))
		m.MedianCycleMinutes = percentile(cycles, .5)
		m.P90CycleMinutes = percentile(cycles, .9)
		m.P95CycleMinutes = percentile(cycles, .95)
	}
	for _, st := range cases {
		_ = st
	} // all queued work is drained, normally empty
	valueAdded := 0.0
	for id, el := range elements {
		m.MeanProcessing += el.service
		if c.scenario.Activities[id].ValueAdded {
			valueAdded += el.service
		}
	}
	if m.Completed > 0 {
		m.MeanProcessing /= float64(m.Completed)
		m.MeanWaiting = m.MeanCycleMinutes - m.MeanProcessing
		if m.MeanCycleMinutes > 0 {
			m.CycleTimeEfficiency = valueAdded / float64(m.Completed) / m.MeanCycleMinutes
		}
	}
	if finalAt > 0 {
		m.ThroughputPerHour = float64(m.Completed) / (finalAt / 60)
	}
	for id, r := range resources {
		updateArea(id, r, finalAt)
		u := 0.0
		if r.capacityMinutes > 0 {
			u = r.busyMinutes / r.capacityMinutes
		}
		ql := 0.0
		if finalAt > 0 {
			ql = r.queueArea / finalAt
		}
		m.Resources = append(m.Resources, ResourceMetrics{ResourceID: id, Name: r.resource.Name, Capacity: r.resource.Capacity, Utilization: u, MeanQueueLength: ql, MaximumQueueSize: r.maxQueue, BusyMinutes: r.busyMinutes})
	}
	sort.Slice(m.Resources, func(i, j int) bool { return m.Resources[i].ResourceID < m.Resources[j].ResourceID })
	for id, el := range elements {
		eq, es := 0.0, 0.0
		if el.completed > 0 {
			eq = el.queue / float64(el.completed)
			es = el.service / float64(el.completed)
		}
		m.Elements = append(m.Elements, ElementMetrics{ElementID: id, ElementName: el.name, Completed: el.completed, MeanQueueMinutes: eq, MeanServiceMinute: es})
	}
	sort.Slice(m.Elements, func(i, j int) bool { return m.Elements[i].ElementID < m.Elements[j].ElementID })
	// derive cost from resource busy time per completed case
	if m.Completed > 0 {
		for _, r := range resources {
			m.MeanCost += r.busyMinutes / 60 * r.resource.CostPerHour / float64(m.Completed)
		}
	}
	return m, nil
}

func routeAfterActivity(c *compiledScenario, node *engine.Node, caseID int, at float64, rng *rand.Rand, frames []syncFrame, push func(simEvent), nextGroupID *int64, cases map[int]*caseState) error {
	events := c.boundaries[node.ID]
	if len(events) > 0 {
		draw, cumulative := rng.Float64(), 0.0
		for _, event := range events {
			for _, flow := range event.Outgoing {
				cumulative += c.scenario.Flows[flow.ID].Probability
				if draw <= cumulative {
					push(simEvent{at: at, kind: eventEnter, caseID: caseID, nodeID: flow.TargetRef, frames: frames})
					return nil
				}
			}
		}
	}
	return route(c, node, caseID, at, rng, frames, push, nextGroupID, cases)
}

func route(c *compiledScenario, node *engine.Node, caseID int, at float64, rng *rand.Rand, frames []syncFrame, push func(simEvent), nextGroupID *int64, cases map[int]*caseState) error {
	n := c.graph.Nodes[node.ID]
	if len(n.Outgoing) == 0 {
		return nil
	}
	if len(n.Outgoing) > 1 && (n.Type == engine.ParallelGatewayType || n.Type == engine.InclusiveGatewayType) {
		selected := make([]engine.Flow, 0, len(n.Outgoing))
		if n.Type == engine.ParallelGatewayType {
			selected = append(selected, n.Outgoing...)
		} else {
			best := n.Outgoing[0]
			bestProbability := -1.0
			for _, flow := range n.Outgoing {
				probability := c.scenario.Flows[flow.ID].Probability
				if probability > bestProbability {
					best, bestProbability = flow, probability
				}
				if rng.Float64() < probability {
					selected = append(selected, flow)
				}
			}
			if len(selected) == 0 {
				selected = append(selected, best)
			}
		}
		*nextGroupID = *nextGroupID + 1
		groupID := *nextGroupID
		joinID := c.gatewayPairs[n.ID]
		cases[caseID].joins[groupID] = &joinState{expected: len(selected)}
		for _, flow := range selected {
			branchFrames := append(append([]syncFrame(nil), frames...), syncFrame{joinID: joinID, groupID: groupID})
			push(simEvent{at: at, kind: eventEnter, caseID: caseID, nodeID: flow.TargetRef, frames: branchFrames})
		}
		return nil
	}
	u := rng.Float64()
	sum := 0.0
	chosen := n.Outgoing[len(n.Outgoing)-1]
	for _, f := range n.Outgoing {
		sum += c.probability(n, f)
		if u <= sum {
			chosen = f
			break
		}
	}
	push(simEvent{at: at, kind: eventEnter, caseID: caseID, nodeID: chosen.TargetRef, frames: frames})
	return nil
}

func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	x := p * float64(len(v)-1)
	lo := int(math.Floor(x))
	hi := int(math.Ceil(x))
	if lo == hi {
		return v[lo]
	}
	return v[lo] + (v[hi]-v[lo])*(x-float64(lo))
}
func confidence(r []RunMetrics, get func(RunMetrics) float64) ConfidenceInterval {
	ci := ConfidenceInterval{}
	if len(r) == 0 {
		return ci
	}
	values := make([]float64, len(r))
	for i, m := range r {
		values[i] = get(m)
		ci.Mean += values[i]
	}
	ci.Mean /= float64(len(r))
	if len(r) == 1 {
		ci.Lower = ci.Mean
		ci.Upper = ci.Mean
		return ci
	}
	variance := 0.0
	for _, v := range values {
		variance += (v - ci.Mean) * (v - ci.Mean)
	}
	variance /= float64(len(r) - 1)
	margin := 1.96 * math.Sqrt(variance/float64(len(r)))
	ci.Lower = math.Max(0, ci.Mean-margin)
	ci.Upper = ci.Mean + margin
	return ci
}
func averageRuns(r []RunMetrics) RunMetrics {
	if len(r) == 0 {
		return RunMetrics{}
	}
	m := RunMetrics{}
	for _, x := range r {
		m.Arrived += x.Arrived
		m.Completed += x.Completed
		m.ThroughputPerHour += x.ThroughputPerHour
		m.MeanCycleMinutes += x.MeanCycleMinutes
		m.MedianCycleMinutes += x.MedianCycleMinutes
		m.P90CycleMinutes += x.P90CycleMinutes
		m.P95CycleMinutes += x.P95CycleMinutes
		m.MeanProcessing += x.MeanProcessing
		m.MeanWaiting += x.MeanWaiting
		m.CycleTimeEfficiency += x.CycleTimeEfficiency
		m.MeanCost += x.MeanCost
	}
	n := float64(len(r))
	m.Arrived /= len(r)
	m.Completed /= len(r)
	m.ThroughputPerHour /= n
	m.MeanCycleMinutes /= n
	m.MedianCycleMinutes /= n
	m.P90CycleMinutes /= n
	m.P95CycleMinutes /= n
	m.MeanProcessing /= n
	m.MeanWaiting /= n
	m.CycleTimeEfficiency /= n
	m.MeanCost /= n
	resourceTotals := map[string]ResourceMetrics{}
	elementTotals := map[string]ElementMetrics{}
	for _, run := range r {
		for _, resource := range run.Resources {
			total := resourceTotals[resource.ResourceID]
			total.ResourceID, total.Name, total.Capacity = resource.ResourceID, resource.Name, resource.Capacity
			total.Utilization += resource.Utilization
			total.MeanQueueLength += resource.MeanQueueLength
			total.MaximumQueueSize += resource.MaximumQueueSize
			total.BusyMinutes += resource.BusyMinutes
			resourceTotals[resource.ResourceID] = total
		}
		for _, element := range run.Elements {
			total := elementTotals[element.ElementID]
			total.ElementID, total.ElementName = element.ElementID, element.ElementName
			total.Completed += element.Completed
			total.MeanQueueMinutes += element.MeanQueueMinutes
			total.MeanServiceMinute += element.MeanServiceMinute
			elementTotals[element.ElementID] = total
		}
	}
	for _, total := range resourceTotals {
		total.Utilization /= n
		total.MeanQueueLength /= n
		total.MaximumQueueSize /= len(r)
		total.BusyMinutes /= n
		m.Resources = append(m.Resources, total)
	}
	for _, total := range elementTotals {
		total.Completed /= len(r)
		total.MeanQueueMinutes /= n
		total.MeanServiceMinute /= n
		m.Elements = append(m.Elements, total)
	}
	sort.Slice(m.Resources, func(i, j int) bool { return m.Resources[i].ResourceID < m.Resources[j].ResourceID })
	sort.Slice(m.Elements, func(i, j int) bool { return m.Elements[i].ElementID < m.Elements[j].ElementID })
	return m
}
