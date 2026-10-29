package simulation

import "time"

type Distribution struct {
	Type              string  `json:"type"`
	Value             float64 `json:"value,omitempty"`
	Mean              float64 `json:"mean,omitempty"`
	StandardDeviation float64 `json:"standardDeviation,omitempty"`
	Min               float64 `json:"min,omitempty"`
	Max               float64 `json:"max,omitempty"`
	Mode              float64 `json:"mode,omitempty"`
}

type Resource struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Capacity    int      `json:"capacity"`
	CostPerHour float64  `json:"costPerHour"`
	CalendarID  string   `json:"calendarId,omitempty"`
	Skills      []string `json:"skills,omitempty"`
}

type ActivityParameters struct {
	ProcessingTime Distribution `json:"processingTime"`
	WaitingTime    Distribution `json:"waitingTime,omitempty"`
	ResourceID     string       `json:"resourceId,omitempty"`
	ResourceIDs    []string     `json:"resourceIds,omitempty"`
	RequiredSkills []string     `json:"requiredSkills,omitempty"`
	RoutingRule    string       `json:"routingRule,omitempty"`
	Priority       int          `json:"priority,omitempty"`
	ValueAdded     bool         `json:"valueAdded"`
}

// BusinessCalendar expresses recurring local-time availability. Weekdays use
// ISO numbering (1=Monday, 7=Sunday). A shift capacity of zero uses the
// resource's configured capacity. Exceptions replace the recurring shifts for
// their local date and can therefore model holidays or exceptional coverage.
type BusinessCalendar struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Timezone   string              `json:"timezone"`
	Shifts     []CalendarShift     `json:"shifts"`
	Exceptions []CalendarException `json:"exceptions,omitempty"`
}

type CalendarShift struct {
	Weekdays    []int `json:"weekdays,omitempty"`
	StartMinute int   `json:"startMinute"`
	EndMinute   int   `json:"endMinute"`
	Capacity    int   `json:"capacity,omitempty"`
}

type CalendarException struct {
	Date   string          `json:"date"`
	Shifts []CalendarShift `json:"shifts,omitempty"`
}

// ArrivalPattern supplements the legacy constant Poisson rate. Supported
// types are poisson, piecewise_poisson, scheduled, and batch_poisson.
type ArrivalPattern struct {
	Type             string           `json:"type"`
	RatePerHour      float64          `json:"ratePerHour,omitempty"`
	Segments         []ArrivalSegment `json:"segments,omitempty"`
	ScheduledMinutes []float64        `json:"scheduledMinutes,omitempty"`
	BatchSize        int              `json:"batchSize,omitempty"`
}

type ArrivalSegment struct {
	StartMinute float64 `json:"startMinute"`
	EndMinute   float64 `json:"endMinute"`
	RatePerHour float64 `json:"ratePerHour"`
}

type FlowParameters struct {
	Probability float64 `json:"probability"`
}

type Scenario struct {
	Name               string                        `json:"name"`
	BPMNXML            string                        `json:"bpmnXml"`
	ArrivalRatePerHour float64                       `json:"arrivalRatePerHour"`
	DurationHours      float64                       `json:"durationHours"`
	MaxArrivals        int                           `json:"maxArrivals,omitempty"`
	Replications       int                           `json:"replications"`
	Seed               int64                         `json:"seed"`
	StartTime          string                        `json:"startTime,omitempty"`
	ArrivalPattern     *ArrivalPattern               `json:"arrivalPattern,omitempty"`
	Calendars          []BusinessCalendar            `json:"calendars,omitempty"`
	Resources          []Resource                    `json:"resources"`
	Activities         map[string]ActivityParameters `json:"activities"`
	Flows              map[string]FlowParameters     `json:"flows"`
}

type AnalyticElement struct {
	ElementID           string  `json:"elementId"`
	ElementName         string  `json:"elementName"`
	ExpectedVisits      float64 `json:"expectedVisits"`
	ProcessingMinutes   float64 `json:"processingMinutes"`
	ExplicitWaitMinutes float64 `json:"explicitWaitMinutes"`
	ExpectedCost        float64 `json:"expectedCost"`
	ResourceID          string  `json:"resourceId,omitempty"`
	ValueAddedMinutes   float64 `json:"valueAddedMinutes"`
}

type AnalyticResult struct {
	ProcessingMinutes   float64           `json:"processingMinutes"`
	WaitingMinutes      float64           `json:"waitingMinutes"`
	CycleTimeMinutes    float64           `json:"cycleTimeMinutes"`
	ValueAddedMinutes   float64           `json:"valueAddedMinutes"`
	CycleTimeEfficiency float64           `json:"cycleTimeEfficiency"`
	CostPerExecution    float64           `json:"costPerExecution"`
	Elements            []AnalyticElement `json:"elements"`
}

type StaffingPoint struct {
	Servers           int     `json:"servers"`
	Utilization       float64 `json:"utilization"`
	ProbabilityOfWait float64 `json:"probabilityOfWait"`
	MeanWaitingHours  float64 `json:"meanWaitingHours"`
	MeanSystemHours   float64 `json:"meanSystemHours"`
	MeetsTarget       bool    `json:"meetsTarget"`
	Stable            bool    `json:"stable"`
}

type StaffingRequest struct {
	ArrivalRatePerHour float64 `json:"arrivalRatePerHour"`
	ServiceTimeHours   float64 `json:"serviceTimeHours"`
	TargetWaitingHours float64 `json:"targetWaitingHours"`
	MaxServers         int     `json:"maxServers"`
}

type StaffingResult struct {
	MinimumServers int             `json:"minimumServers,omitempty"`
	Points         []StaffingPoint `json:"points"`
}

type ResourceMetrics struct {
	ResourceID       string  `json:"resourceId"`
	Name             string  `json:"name"`
	Capacity         int     `json:"capacity"`
	Utilization      float64 `json:"utilization"`
	MeanQueueLength  float64 `json:"meanQueueLength"`
	MaximumQueueSize int     `json:"maximumQueueSize"`
	BusyMinutes      float64 `json:"busyMinutes"`
}

type ElementMetrics struct {
	ElementID         string  `json:"elementId"`
	ElementName       string  `json:"elementName"`
	Completed         int     `json:"completed"`
	MeanQueueMinutes  float64 `json:"meanQueueMinutes"`
	MeanServiceMinute float64 `json:"meanServiceMinutes"`
}

type RunMetrics struct {
	Arrived             int               `json:"arrived"`
	Completed           int               `json:"completed"`
	ThroughputPerHour   float64           `json:"throughputPerHour"`
	MeanCycleMinutes    float64           `json:"meanCycleMinutes"`
	MedianCycleMinutes  float64           `json:"medianCycleMinutes"`
	P90CycleMinutes     float64           `json:"p90CycleMinutes"`
	P95CycleMinutes     float64           `json:"p95CycleMinutes"`
	MeanProcessing      float64           `json:"meanProcessingMinutes"`
	MeanWaiting         float64           `json:"meanWaitingMinutes"`
	CycleTimeEfficiency float64           `json:"cycleTimeEfficiency"`
	MeanCost            float64           `json:"meanCost"`
	Resources           []ResourceMetrics `json:"resources"`
	Elements            []ElementMetrics  `json:"elements"`
}

type ConfidenceInterval struct {
	Mean  float64 `json:"mean"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type SimulationResult struct {
	ScenarioName      string                   `json:"scenarioName"`
	GeneratedAt       time.Time                `json:"generatedAt"`
	Replications      int                      `json:"replications"`
	Metrics           RunMetrics               `json:"metrics"`
	CycleTime95       ConfidenceInterval       `json:"cycleTime95"`
	Cost95            ConfidenceInterval       `json:"cost95"`
	Throughput95      ConfidenceInterval       `json:"throughput95"`
	Analytic          AnalyticResult           `json:"analytic"`
	Issues            []Issue                  `json:"issues"`
	RecommendedDesign []RedesignRecommendation `json:"recommendedDesign"`
}

type Issue struct {
	Code       string  `json:"code"`
	ElementID  string  `json:"elementId,omitempty"`
	Title      string  `json:"title"`
	Evidence   string  `json:"evidence"`
	Category   string  `json:"category"`
	Severity   string  `json:"severity"`
	Metric     float64 `json:"metric,omitempty"`
	Suggestion string  `json:"suggestion"`
}

type RedesignRecommendation struct {
	Heuristic       string   `json:"heuristic"`
	Title           string   `json:"title"`
	Justification   string   `json:"justification"`
	AddressesIssues []string `json:"addressesIssues"`
}

type CapacitySweepRequest struct {
	ResourceID             string  `json:"resourceId"`
	MinimumCapacity        int     `json:"minimumCapacity"`
	MaximumCapacity        int     `json:"maximumCapacity"`
	Step                   int     `json:"step"`
	TargetMeanCycleMinutes float64 `json:"targetMeanCycleMinutes,omitempty"`
	TargetP95CycleMinutes  float64 `json:"targetP95CycleMinutes,omitempty"`
}

type CapacitySweepPoint struct {
	Capacity          int     `json:"capacity"`
	MeanCycleMinutes  float64 `json:"meanCycleMinutes"`
	P95CycleMinutes   float64 `json:"p95CycleMinutes"`
	ThroughputPerHour float64 `json:"throughputPerHour"`
	MeanCost          float64 `json:"meanCost"`
	Utilization       float64 `json:"utilization"`
	MeanQueueLength   float64 `json:"meanQueueLength"`
	MaximumQueueSize  int     `json:"maximumQueueSize"`
	MeetsTarget       bool    `json:"meetsTarget"`
}

type CapacitySweepResult struct {
	ScenarioName        string               `json:"scenarioName"`
	ResourceID          string               `json:"resourceId"`
	RecommendedCapacity int                  `json:"recommendedCapacity,omitempty"`
	Points              []CapacitySweepPoint `json:"points"`
}
