package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	pb "github.com/jeremielodi/bpmngo/internal/zeebegrpc/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type behaviorResult struct {
	Engine      string           `json:"engine"`
	GeneratedAt string           `json:"generated_at"`
	Scenarios   []scenarioResult `json:"scenarios"`
}

type scenarioResult struct {
	Name         string         `json:"name"`
	Category     string         `json:"category"`
	DeployCode   string         `json:"deploy_code"`
	StartCode    string         `json:"start_code,omitempty"`
	Observations map[string]any `json:"observations,omitempty"`
	Error        string         `json:"error,omitempty"`
}

type scenario struct {
	name, category, processID, model string
	variables                        string
	skipStart                        bool
	run                              func(*runner, int64) (map[string]any, error)
}

func (r *runner) behavior() behaviorResult {
	result := behaviorResult{Engine: r.cfg.engine, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for i, sc := range behaviorScenarios() {
		row := scenarioResult{Name: sc.name, Category: sc.category, Observations: map[string]any{}}
		name := fmt.Sprintf("behavior-%02d-%s.bpmn", i+1, safeID(sc.name))
		_, err := r.deploy(name, sc.model)
		row.DeployCode = grpcCode(err)
		if err != nil {
			row.Error = err.Error()
			result.Scenarios = append(result.Scenarios, row)
			continue
		}
		vars := sc.variables
		if vars == "" {
			vars = `{}`
		}
		var instanceKey int64
		if !sc.skipStart {
			ctx, cancel := r.ctx(10 * time.Second)
			created, err := r.client.CreateProcessInstance(ctx, &pb.CreateProcessInstanceRequest{BpmnProcessId: sc.processID, Version: -1, Variables: vars})
			cancel()
			row.StartCode = grpcCode(err)
			if err != nil {
				row.Error = err.Error()
				result.Scenarios = append(result.Scenarios, row)
				continue
			}
			instanceKey = created.GetProcessInstanceKey()
		} else {
			row.StartCode = "SKIPPED_EVENT_START"
		}
		obs, err := sc.run(r, instanceKey)
		row.Observations = obs
		if err != nil {
			row.Error = err.Error()
		}
		// Every scenario is isolated; cancel is deliberately best effort because
		// completed instances return NOT_FOUND on both engines.
		if instanceKey != 0 {
			ctx, cancel := r.ctx(5 * time.Second)
			_, _ = r.client.CancelProcessInstance(ctx, &pb.CancelProcessInstanceRequest{ProcessInstanceKey: instanceKey})
			cancel()
		}
		result.Scenarios = append(result.Scenarios, row)
	}
	return result
}

func behaviorScenarios() []scenario {
	var out []scenario
	addRoute := func(name, category, gateway, flows, vars string, want []string) {
		id := fmt.Sprintf("B%02d", len(out)+1)
		out = append(out, scenario{name: name, category: category, processID: id, variables: vars, model: routeModel(id, gateway, flows), run: expectJobs(want...)})
	}
	addRoute("exclusive-one-true", "gateway", "exclusive", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x = 1</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x = 2</bpmn:conditionExpression></bpmn:sequenceFlow>`, `{"x":1}`, []string{"a"})
	addRoute("exclusive-default", "gateway", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x = 1</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{"x":9}`, []string{"b"})
	addRoute("exclusive-two-true-reference-order", "gateway", "exclusive-explicit-b-first", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x &gt; 0</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x &gt; 0</bpmn:conditionExpression></bpmn:sequenceFlow>`, `{"x":1}`, []string{"b"})
	addRoute("inclusive-two-true", "gateway", "inclusive", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x &gt; 0</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x &lt; 10</bpmn:conditionExpression></bpmn:sequenceFlow>`, `{"x":5}`, []string{"a", "b"})
	addRoute("inclusive-default", "gateway", "inclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= x = 1</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{"x":9}`, []string{"b"})
	out = append(out, parallelScenario(fmt.Sprintf("B%02d", len(out)+1)))
	addRoute("feel-number", "feel", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= price * qty = 12.5</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{"price":2.5,"qty":5}`, []string{"a"})
	addRoute("feel-null", "feel", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= missing = null</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{}`, []string{"a"})
	addRoute("feel-string", "feel", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= upper case(name) = &quot;ADA&quot;</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{"name":"Ada"}`, []string{"a"})
	addRoute("feel-list", "feel", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= count(items[price &gt; 2]) = 2</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{"items":[{"price":1},{"price":3},{"price":4}]}`, []string{"a"})
	addRoute("feel-context", "feel", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= customer.address.country = &quot;BE&quot;</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{"customer":{"address":{"country":"BE"}}}`, []string{"a"})
	addRoute("feel-date-duration", "feel", "exclusive-default", `<bpmn:sequenceFlow id="toA" sourceRef="gw" targetRef="a"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">= date(&quot;2026-01-02&quot;) &gt; date(&quot;2026-01-01&quot;) and duration(&quot;P2D&quot;) &gt; duration(&quot;P1D&quot;)</bpmn:conditionExpression></bpmn:sequenceFlow><bpmn:sequenceFlow id="toB" sourceRef="gw" targetRef="b"/>`, `{}`, []string{"a"})

	return append(out, eventAndJobScenarios(len(out))...)
}

func routeModel(id, gateway, flows string) string {
	gwTag := "exclusiveGateway"
	attrs := ""
	if strings.HasPrefix(gateway, "inclusive") {
		gwTag = "inclusiveGateway"
	}
	if strings.HasSuffix(gateway, "default") {
		attrs = ` default="toB"`
	}
	gw := `<bpmn:` + gwTag + ` id="gw"` + attrs + `/>`
	if gateway == "exclusive-explicit-b-first" {
		gw = `<bpmn:exclusiveGateway id="gw"><bpmn:outgoing>toB</bpmn:outgoing><bpmn:outgoing>toA</bpmn:outgoing></bpmn:exclusiveGateway>`
	}
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="begin" sourceRef="start" targetRef="gw"/>` + gw + flows + jobNode("a", "route-a") + jobNode("b", "route-b")
	return wrapDefinitions(id, body)
}

func parallelScenario(id string) scenario {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f0" sourceRef="start" targetRef="split"/><bpmn:parallelGateway id="split"/><bpmn:sequenceFlow id="fa" sourceRef="split" targetRef="a"/><bpmn:sequenceFlow id="fb" sourceRef="split" targetRef="b"/>` + jobNode("a", "route-a") + jobNode("b", "route-b")
	return scenario{name: "parallel-split", category: "gateway", processID: id, model: wrapDefinitions(id, body), run: expectJobs("a", "b")}
}

func expectJobs(elements ...string) func(*runner, int64) (map[string]any, error) {
	return func(r *runner, instance int64) (map[string]any, error) {
		var got []map[string]any
		for _, typ := range []string{"route-a", "route-b"} {
			jobs, _ := r.activateFor(typ, instance, 900*time.Millisecond, 10)
			for _, j := range jobs {
				got = append(got, jobObservation(j))
			}
		}
		sort.Slice(got, func(i, j int) bool { return fmt.Sprint(got[i]["elementId"]) < fmt.Sprint(got[j]["elementId"]) })
		want := append([]string(nil), elements...)
		sort.Strings(want)
		gotIDs := make([]string, len(got))
		for i := range got {
			gotIDs[i] = fmt.Sprint(got[i]["elementId"])
		}
		obs := map[string]any{"jobs": got, "expected_elements": want, "observed_elements": gotIDs}
		if strings.Join(want, ",") != strings.Join(gotIDs, ",") {
			return obs, fmt.Errorf("expected jobs %v, got %v", want, gotIDs)
		}
		return obs, nil
	}
}

func (r *runner) activateFor(jobType string, instance int64, timeout time.Duration, max int32) ([]*pb.ActivatedJob, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := r.ctx(minDuration(2*time.Second, time.Until(deadline)))
		stream, err := r.client.ActivateJobs(ctx, &pb.ActivateJobsRequest{Type: jobType, Worker: "behavior-worker", Timeout: 30000, MaxJobsToActivate: max, RequestTimeout: 500})
		if err != nil {
			cancel()
			continue
		}
		resp, err := stream.Recv()
		cancel()
		if err != nil {
			if status.Code(err) == codes.DeadlineExceeded {
				continue
			}
			continue
		}
		var matched []*pb.ActivatedJob
		for _, j := range resp.GetJobs() {
			if j.GetProcessInstanceKey() == instance {
				matched = append(matched, j)
			}
		}
		if len(matched) > 0 {
			return matched, nil
		}
	}
	return nil, status.Error(codes.DeadlineExceeded, "no matching job")
}

func jobObservation(j *pb.ActivatedJob) map[string]any {
	return map[string]any{"key": fmt.Sprint(j.GetKey()), "type": j.GetType(), "elementId": j.GetElementId(), "retries": j.GetRetries(), "variables": jsonValue(j.GetVariables()), "headers": jsonValue(j.GetCustomHeaders())}
}
func jsonValue(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func wrapDefinitions(id, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="D_` + id + `" targetNamespace="http://fair-benchmark/` + id + `"><bpmn:process id="` + id + `" isExecutable="true">` + body + `</bpmn:process></bpmn:definitions>`
}
func jobNode(id, typ string) string {
	return `<bpmn:serviceTask id="` + id + `"><bpmn:extensionElements><zeebe:taskDefinition type="` + typ + `" retries="3"/></bpmn:extensionElements></bpmn:serviceTask>`
}

// restJSON is shared by the REST-only scenarios added below. Camunda's
// lightweight distribution is unprotected; BpmnGo receives the exact Basic
// credentials specified by the benchmark prompt.
func (r *runner) restJSON(method, path string, body any) (int, map[string]any, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, strings.TrimRight(r.cfg.rest, "/")+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.cfg.user != "" {
		req.SetBasicAuth(r.cfg.user, r.cfg.password)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, out, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return resp.StatusCode, out, nil
}
