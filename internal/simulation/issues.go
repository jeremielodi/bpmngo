package simulation

import "fmt"

// DetectIssues turns simulation evidence into an editable issue-register seed.
func DetectIssues(result SimulationResult) []Issue {
	issues := make([]Issue, 0)
	for _, resource := range result.Metrics.Resources {
		if resource.Utilization >= .85 {
			severity := "medium"
			if resource.Utilization >= .95 {
				severity = "high"
			}
			issues = append(issues, Issue{
				Code: "RESOURCE_SATURATION_" + resource.ResourceID, Title: "Resource saturation: " + resource.Name,
				Category: "capacity", Severity: severity, Metric: resource.Utilization,
				Evidence:   fmt.Sprintf("Utilization is %.1f%% and the maximum queue is %d.", resource.Utilization*100, resource.MaximumQueueSize),
				Suggestion: "Test additional capacity, skill pooling, or demand smoothing.",
			})
		}
	}
	for _, element := range result.Metrics.Elements {
		if element.MeanQueueMinutes >= 120 {
			issues = append(issues, Issue{
				Code: "LONG_QUEUE_" + element.ElementID, ElementID: element.ElementID, Title: "Long queue before " + element.ElementName,
				Category: "waiting", Severity: "high", Metric: element.MeanQueueMinutes,
				Evidence:   fmt.Sprintf("Mean queue time is %.1f minutes.", element.MeanQueueMinutes),
				Suggestion: "Remove handoffs, rebalance capacity, or triage work before this activity.",
			})
		}
	}
	if result.Metrics.CycleTimeEfficiency > 0 && result.Metrics.CycleTimeEfficiency < .1 {
		issues = append(issues, Issue{
			Code: "LOW_CYCLE_EFFICIENCY", Title: "Low cycle-time efficiency", Category: "time", Severity: "high",
			Metric:     result.Metrics.CycleTimeEfficiency,
			Evidence:   fmt.Sprintf("Only %.1f%% of cycle time is processing time.", result.Metrics.CycleTimeEfficiency*100),
			Suggestion: "Prioritize removal of waits and handoffs before automating task execution.",
		})
	}
	return issues
}

func RecommendRedesign(issues []Issue) []RedesignRecommendation {
	if len(issues) == 0 {
		return []RedesignRecommendation{{Heuristic: "control", Title: "Retain the design and monitor it", Justification: "No configured threshold was exceeded."}}
	}
	codes := make([]string, 0, len(issues))
	hasCapacity, hasWaiting := false, false
	for _, issue := range issues {
		codes = append(codes, issue.Code)
		hasCapacity = hasCapacity || issue.Category == "capacity"
		hasWaiting = hasWaiting || issue.Category == "waiting" || issue.Code == "LOW_CYCLE_EFFICIENCY"
	}
	recommendations := make([]RedesignRecommendation, 0, 3)
	if hasCapacity {
		recommendations = append(recommendations, RedesignRecommendation{Heuristic: "resource-optimization", Title: "Pool skills and rebalance capacity", Justification: "High utilization makes queues grow non-linearly; test capacity before committing staffing changes.", AddressesIssues: codes})
	}
	if hasWaiting {
		recommendations = append(recommendations, RedesignRecommendation{Heuristic: "integration", Title: "Remove handoffs and batch delays", Justification: "The measured cycle time is dominated by waiting rather than work.", AddressesIssues: codes})
	}
	return recommendations
}
