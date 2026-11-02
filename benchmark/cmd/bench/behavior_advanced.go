package main

import (
	"fmt"
	"net/http"
	"time"

	pb "github.com/jeremielodi/bpmngo/internal/zeebegrpc/pb"
)

func advancedScenarios(start int) []scenario {
	var out []scenario
	next := func() string { start++; return fmt.Sprintf("B%02d", start) }

	id := next()
	out = append(out, scenario{name: "event-subprocess-message", category: "event-subprocess", processID: id, variables: `{"orderId":"EVENTSUB"}`, model: eventSubprocessModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			main, mainErr := r.activateFor("main-work", pi, 2*time.Second, 1)
			if mainErr != nil {
				return nil, mainErr
			}
			time.Sleep(time.Second)
			_, publication, err := r.restJSON("POST", "/v2/messages/publication?physicalTenantId=default", map[string]any{"name": "EventSub" + sid, "correlationKey": "EVENTSUB", "timeToLive": 5000, "messageId": fmt.Sprintf("es-%d", pi), "variables": map[string]any{}})
			if err != nil {
				return nil, err
			}
			jobs, jobErr := r.activateFor("event-sub-job", pi, 2*time.Second, 1)
			obs := map[string]any{"main": jobObservation(main[0]), "event_count": len(jobs), "publication": publication}
			if len(jobs) > 0 {
				obs["event"] = jobObservation(jobs[0])
			}
			return obs, jobErr
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "multi-instance-parallel", category: "multi-instance", processID: id, variables: `{"items":[3,1,2]}`, model: multiInstanceModel(id, "parallel", false), run: observeCount("mi-job", 3)})
	id = next()
	out = append(out, scenario{name: "multi-instance-sequential", category: "multi-instance", processID: id, variables: `{"items":[3,1,2]}`, model: multiInstanceModel(id, "sequential", true), run: sequentialMI})
	id = next()
	out = append(out, scenario{name: "multi-instance-output-collection", category: "multi-instance", processID: id, variables: `{"items":[3,1,2]}`, model: multiInstanceOutputModel(id), run: multiOutput})
	id = next()
	out = append(out, scenario{name: "multi-instance-completion-condition", category: "multi-instance", processID: id, variables: `{"items":[1,2,3]}`, model: multiCompletionModel(id), run: multiCompletion})
	id = next()
	out = append(out, scenario{name: "multi-instance-empty-collection", category: "multi-instance", processID: id, variables: `{"items":[]}`, model: multiInstanceModel(id, "empty", false), run: observeOne("mi-after", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "call-activity", category: "call-activity", processID: id + "Parent", variables: `{"amount":21}`, model: callActivityModel(id), run: func(r *runner, _ int64) (map[string]any, error) {
		jobs, err := r.activateAny("child-job", 2*time.Second, 1)
		obs := map[string]any{"count": len(jobs)}
		if len(jobs) > 0 {
			obs["child"] = jobObservation(jobs[0])
		}
		return obs, err
	}})
	id = next()
	out = append(out, scenario{name: "message-correlation-key", category: "messages", processID: id, variables: `{"orderId":"A-42"}`, model: messageCatchModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			time.Sleep(time.Second)
			_, publication, err := r.restJSON("POST", "/v2/messages/publication?physicalTenantId=default", map[string]any{"name": "Paid" + sid, "correlationKey": "A-42", "timeToLive": 5000, "messageId": "paid-A-42-" + sid, "variables": map[string]any{"paid": true}})
			if err != nil {
				return map[string]any{"publish_code": grpcCode(err)}, err
			}
			obs, e := observeOne("message-after", 2*time.Second)(r, pi)
			obs["publication"] = publication
			return obs, e
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "message-id-deduplication", category: "messages", processID: id, variables: `{"orderId":"D-1"}`, model: messageCatchModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			time.Sleep(time.Second)
			codes := []string{}
			for n := 0; n < 2; n++ {
				code, _, err := r.restJSON("POST", "/v2/messages/publication?physicalTenantId=default", map[string]any{"name": "Paid" + sid, "correlationKey": "D-1", "timeToLive": 5000, "messageId": "same-id-" + sid, "variables": map[string]any{}})
				codes = append(codes, fmt.Sprintf("HTTP_%d_%v", code, err == nil))
			}
			obs, e := observeOne("message-after", 2*time.Second)(r, pi)
			obs["publish_codes"] = codes
			return obs, e
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "message-start-event", category: "messages", processID: id, skipStart: true, model: messageStartModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, _ int64) (map[string]any, error) {
			time.Sleep(time.Second)
			_, publication, err := r.restJSON("POST", "/v2/messages/publication?physicalTenantId=default", map[string]any{"name": "Begin" + sid, "correlationKey": "S-1", "timeToLive": 5000, "messageId": "start-S-1-" + sid, "variables": map[string]any{"orderId": "S-1"}})
			if err != nil {
				return nil, err
			}
			jobs, err := r.activateAny("message-started", 2*time.Second, 10)
			obs := map[string]any{"count": len(jobs), "publication": publication}
			if len(jobs) > 0 {
				obs["job"] = jobObservation(jobs[0])
			}
			return obs, err
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "event-based-gateway-message", category: "messages", processID: id, variables: `{"orderId":"E-1"}`, model: eventGatewayModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			time.Sleep(time.Second)
			_, publication, err := r.restJSON("POST", "/v2/messages/publication?physicalTenantId=default", map[string]any{"name": "Approve" + sid, "correlationKey": "E-1", "timeToLive": 5000, "messageId": "approve-E-1-" + sid, "variables": map[string]any{}})
			if err != nil {
				return nil, err
			}
			obs, observeErr := observeOne("event-message", 2*time.Second)(r, pi)
			obs["publication"] = publication
			return obs, observeErr
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "signal-intermediate-catch", category: "signals", processID: id, model: signalCatchModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			time.Sleep(time.Second)
			_, broadcast, err := r.restJSON("POST", "/v2/signals/broadcast?physicalTenantId=default", map[string]any{"signalName": "Go" + sid, "variables": map[string]any{"signalValue": 9}})
			if err != nil {
				return nil, err
			}
			obs, observeErr := observeOne("signal-after", 2*time.Second)(r, pi)
			obs["broadcast"] = broadcast
			return obs, observeErr
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "timer-duration", category: "timers", processID: id, model: timerModel(id, "timeDuration", "PT0.2S"), run: observeOne("timer-after", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "timer-date-past", category: "timers", processID: id, model: timerModel(id, "timeDate", "2020-01-01T00:00:00Z"), run: observeOne("timer-after", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "timer-cycle", category: "timers", processID: id, model: timerCycleModel(id), run: observeCount("timer-tick", 2)})
	id = next()
	out = append(out, scenario{name: "timer-feel-duration", category: "timers", processID: id, variables: `{"delay":"PT0.2S"}`, model: timerModel(id, "timeDuration", "= delay"), run: observeOne("timer-after", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "user-task-rest-completion", category: "rest-v2", processID: id, model: userTaskModel(id), run: userTaskREST})
	id = next()
	out = append(out, scenario{name: "compensation", category: "compensation", processID: id, model: compensationModel(id), run: compensationRun})
	id = next()
	out = append(out, scenario{name: "grpc-key-usable-in-rest", category: "api", processID: id, variables: `{"kept":"yes"}`, model: singleJobModel(id, "key-job", ""), run: keyInREST})
	id = next()
	out = append(out, scenario{name: "finished-instance-elements-and-variables", category: "api", processID: id, variables: `{"kept":"yes"}`, model: singleJobModel(id, "archive-job", ""), run: finishedSearch})

	return out
}

func (r *runner) activateAny(jobType string, timeout time.Duration, max int32) ([]*pb.ActivatedJob, error) {
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
		if err == nil && len(resp.GetJobs()) > 0 {
			return resp.GetJobs(), nil
		}
	}
	return nil, fmt.Errorf("no %s job observed", jobType)
}
func observeCount(jobType string, want int) func(*runner, int64) (map[string]any, error) {
	return func(r *runner, pi int64) (map[string]any, error) {
		deadline := time.Now().Add(3 * time.Second)
		var all []*pb.ActivatedJob
		for len(all) < want && time.Now().Before(deadline) {
			jobs, _ := r.activateFor(jobType, pi, 700*time.Millisecond, int32(want))
			all = append(all, jobs...)
		}
		obs := map[string]any{"count": len(all), "expected": want}
		var js []map[string]any
		for _, j := range all {
			js = append(js, jobObservation(j))
		}
		obs["jobs"] = js
		if len(all) != want {
			return obs, fmt.Errorf("expected %d jobs, got %d", want, len(all))
		}
		return obs, nil
	}
}
func complete(r *runner, key int64, variables string) error {
	ctx, cancel := r.ctx(5 * time.Second)
	defer cancel()
	_, err := r.client.CompleteJob(ctx, &pb.CompleteJobRequest{JobKey: key, Variables: variables})
	return err
}
func sequentialMI(r *runner, pi int64) (map[string]any, error) {
	var seen []any
	for i := 0; i < 3; i++ {
		jobs, err := r.activateFor("mi-job", pi, 2*time.Second, 1)
		if err != nil {
			return map[string]any{"seen": seen}, err
		}
		seen = append(seen, jsonValue(jobs[0].GetVariables()))
		if err = complete(r, jobs[0].GetKey(), `{}`); err != nil {
			return nil, err
		}
	}
	return map[string]any{"seen": seen}, nil
}
func multiOutput(r *runner, pi int64) (map[string]any, error) {
	jobs, err := r.activateFor("mi-output", pi, 2*time.Second, 3)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		v := jsonValue(j.GetVariables()).(map[string]any)
		if err := complete(r, j.GetKey(), fmt.Sprintf(`{"result":%v}`, v["item"])); err != nil {
			return nil, err
		}
	}
	after, err := r.activateFor("mi-after", pi, 2*time.Second, 1)
	if err != nil {
		return nil, err
	}
	return map[string]any{"after": jobObservation(after[0])}, nil
}
func multiCompletion(r *runner, pi int64) (map[string]any, error) {
	jobs, err := r.activateFor("mi-complete", pi, 2*time.Second, 3)
	if err != nil {
		return nil, err
	}
	completed := 0
	for _, j := range jobs[:minInt(2, len(jobs))] {
		if complete(r, j.GetKey(), `{}`) == nil {
			completed++
		}
	}
	after, afterErr := r.activateFor("mi-after", pi, 2*time.Second, 1)
	obs := map[string]any{"activated": len(jobs), "completed": completed}
	if len(after) > 0 {
		obs["after"] = jobObservation(after[0])
	}
	return obs, afterErr
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func userTaskREST(r *runner, pi int64) (map[string]any, error) {
	deadline := time.Now().Add(5 * time.Second)
	var response map[string]any
	var code int
	for time.Now().Before(deadline) {
		c, res, err := r.restJSON(http.MethodPost, "/v2/user-tasks/search", map[string]any{"filter": map[string]any{"processInstanceKey": fmt.Sprint(pi)}})
		code, response = c, res
		if err == nil {
			if items, ok := res["items"].([]any); ok && len(items) > 0 {
				task := items[0].(map[string]any)
				key := fmt.Sprint(task["userTaskKey"])
				if key == "<nil>" {
					key = fmt.Sprint(task["key"])
				}
				cc, done, e := r.restJSON(http.MethodPost, "/v2/user-tasks/"+key+"/completion", map[string]any{"variables": map[string]any{"approved": true}})
				return map[string]any{"search_status": code, "task": task, "completion_status": cc, "completion": done}, e
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return map[string]any{"search_status": code, "response": response}, fmt.Errorf("user task not observed")
}
func compensationRun(r *runner, pi int64) (map[string]any, error) {
	book, err := r.activateFor("book", pi, 2*time.Second, 1)
	if err != nil {
		return nil, err
	}
	if err = complete(r, book[0].GetKey(), `{}`); err != nil {
		return nil, err
	}
	undo, err := r.activateFor("undo-book", pi, 2*time.Second, 1)
	obs := map[string]any{"book": jobObservation(book[0]), "undo_count": len(undo)}
	if len(undo) > 0 {
		obs["undo"] = jobObservation(undo[0])
	}
	return obs, err
}
func keyInREST(r *runner, pi int64) (map[string]any, error) {
	jobs, err := r.activateFor("key-job", pi, 2*time.Second, 1)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	var code int
	var res map[string]any
	var e error
	for time.Now().Before(deadline) {
		code, res, e = r.restJSON(http.MethodGet, "/v2/process-instances/"+fmt.Sprint(pi), nil)
		if e == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	return map[string]any{"grpc_key": fmt.Sprint(pi), "job": jobObservation(jobs[0]), "rest_status": code, "rest": res}, e
}
func finishedSearch(r *runner, pi int64) (map[string]any, error) {
	jobs, err := r.activateFor("archive-job", pi, 2*time.Second, 1)
	if err != nil {
		return nil, err
	}
	if err = complete(r, jobs[0].GetKey(), `{"done":true}`); err != nil {
		return nil, err
	}
	time.Sleep(1200 * time.Millisecond)
	c1, elements, e1 := r.restJSON(http.MethodPost, "/v2/element-instances/search", map[string]any{"filter": map[string]any{"processInstanceKey": fmt.Sprint(pi)}})
	c2, vars, e2 := r.restJSON(http.MethodPost, "/v2/variables/search", map[string]any{"filter": map[string]any{"processInstanceKey": fmt.Sprint(pi)}})
	obs := map[string]any{"elements_status": c1, "elements": elements, "variables_status": c2, "variables": vars}
	if e1 != nil {
		return obs, e1
	}
	return obs, e2
}

func eventSubprocessModel(id string) string {
	extra := `<bpmn:message id="m_` + id + `" name="EventSub` + id + `"><bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements></bpmn:message>`
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="main"/>` + jobNode("main", "main-work") + `<bpmn:subProcess id="eventSub" triggeredByEvent="true"><bpmn:startEvent id="eventStart" isInterrupting="false"><bpmn:messageEventDefinition messageRef="m_` + id + `"/></bpmn:startEvent><bpmn:sequenceFlow id="ef" sourceRef="eventStart" targetRef="eventJob"/>` + jobNode("eventJob", "event-sub-job") + `</bpmn:subProcess>`
	return wrapCustom(id, extra, body)
}
func multiInstanceModel(id string, mode string, sequential bool) string {
	seq := "false"
	if sequential {
		seq = "true"
	}
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="each"/><bpmn:serviceTask id="each"><bpmn:extensionElements><zeebe:taskDefinition type="mi-job"/></bpmn:extensionElements><bpmn:multiInstanceLoopCharacteristics isSequential="` + seq + `"><bpmn:extensionElements><zeebe:loopCharacteristics inputCollection="= items" inputElement="item"/></bpmn:extensionElements></bpmn:multiInstanceLoopCharacteristics></bpmn:serviceTask><bpmn:sequenceFlow id="f2" sourceRef="each" targetRef="after"/>` + jobNode("after", "mi-after")
	return wrapDefinitions(id, body)
}
func multiInstanceOutputModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="each"/><bpmn:serviceTask id="each"><bpmn:extensionElements><zeebe:taskDefinition type="mi-output"/></bpmn:extensionElements><bpmn:multiInstanceLoopCharacteristics><bpmn:extensionElements><zeebe:loopCharacteristics inputCollection="= items" inputElement="item" outputCollection="results" outputElement="= result"/></bpmn:extensionElements></bpmn:multiInstanceLoopCharacteristics></bpmn:serviceTask><bpmn:sequenceFlow id="f2" sourceRef="each" targetRef="after"/>` + jobNode("after", "mi-after")
	return wrapDefinitions(id, body)
}
func multiCompletionModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="each"/><bpmn:serviceTask id="each"><bpmn:extensionElements><zeebe:taskDefinition type="mi-complete"/></bpmn:extensionElements><bpmn:multiInstanceLoopCharacteristics><bpmn:extensionElements><zeebe:loopCharacteristics inputCollection="= items" inputElement="item"/></bpmn:extensionElements><bpmn:completionCondition>= numberOfCompletedInstances &gt;= 2</bpmn:completionCondition></bpmn:multiInstanceLoopCharacteristics></bpmn:serviceTask><bpmn:sequenceFlow id="f2" sourceRef="each" targetRef="after"/>` + jobNode("after", "mi-after")
	return wrapDefinitions(id, body)
}
func callActivityModel(id string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="D_` + id + `" targetNamespace="http://fair-benchmark/` + id + `"><bpmn:process id="` + id + `Child" isExecutable="true"><bpmn:startEvent id="cs"/><bpmn:sequenceFlow id="cf" sourceRef="cs" targetRef="cj"/>` + jobNode("cj", "child-job") + `</bpmn:process><bpmn:process id="` + id + `Parent" isExecutable="true"><bpmn:startEvent id="ps"/><bpmn:sequenceFlow id="pf" sourceRef="ps" targetRef="call"/><bpmn:callActivity id="call"><bpmn:extensionElements><zeebe:calledElement processId="= &quot;` + id + `Child&quot;" propagateAllChildVariables="true"/></bpmn:extensionElements></bpmn:callActivity></bpmn:process></bpmn:definitions>`
}
func messageCatchModel(id string) string {
	extra := `<bpmn:message id="m_` + id + `" name="Paid` + id + `"><bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements></bpmn:message>`
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="wait"/><bpmn:intermediateCatchEvent id="wait"><bpmn:messageEventDefinition messageRef="m_` + id + `"/></bpmn:intermediateCatchEvent><bpmn:sequenceFlow id="f2" sourceRef="wait" targetRef="after"/>` + jobNode("after", "message-after")
	return wrapCustom(id, extra, body)
}
func messageStartModel(id string) string {
	extra := `<bpmn:message id="m_` + id + `" name="Begin` + id + `"><bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements></bpmn:message>`
	body := `<bpmn:startEvent id="start"><bpmn:messageEventDefinition messageRef="m_` + id + `"/></bpmn:startEvent><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="after"/>` + jobNode("after", "message-started")
	return wrapCustom(id, extra, body)
}
func eventGatewayModel(id string) string {
	extra := `<bpmn:message id="m_` + id + `" name="Approve` + id + `"><bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements></bpmn:message>`
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="gw"/><bpmn:eventBasedGateway id="gw"><bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>fm</bpmn:outgoing><bpmn:outgoing>ft</bpmn:outgoing></bpmn:eventBasedGateway><bpmn:sequenceFlow id="fm" sourceRef="gw" targetRef="msg"/><bpmn:sequenceFlow id="ft" sourceRef="gw" targetRef="timer"/><bpmn:intermediateCatchEvent id="msg"><bpmn:messageEventDefinition messageRef="m_` + id + `"/></bpmn:intermediateCatchEvent><bpmn:intermediateCatchEvent id="timer"><bpmn:timerEventDefinition><bpmn:timeDuration>PT2S</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent><bpmn:sequenceFlow id="fm2" sourceRef="msg" targetRef="mj"/>` + jobNode("mj", "event-message") + `<bpmn:sequenceFlow id="ft2" sourceRef="timer" targetRef="tj"/>` + jobNode("tj", "event-timer")
	return wrapCustom(id, extra, body)
}
func signalCatchModel(id string) string {
	extra := `<bpmn:signal id="s_` + id + `" name="Go` + id + `"/>`
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="wait"/><bpmn:intermediateCatchEvent id="wait"><bpmn:signalEventDefinition signalRef="s_` + id + `"/></bpmn:intermediateCatchEvent><bpmn:sequenceFlow id="f2" sourceRef="wait" targetRef="after"/>` + jobNode("after", "signal-after")
	return wrapCustom(id, extra, body)
}
func timerModel(id, kind, value string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="wait"/><bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:` + kind + `>` + value + `</bpmn:` + kind + `></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent><bpmn:sequenceFlow id="f2" sourceRef="wait" targetRef="after"/>` + jobNode("after", "timer-after")
	return wrapDefinitions(id, body)
}
func timerCycleModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="main"/>` + jobNode("main", "main-work") + `<bpmn:subProcess id="timerSub" triggeredByEvent="true"><bpmn:startEvent id="timerStart" isInterrupting="false"><bpmn:timerEventDefinition><bpmn:timeCycle>R2/PT0.2S</bpmn:timeCycle></bpmn:timerEventDefinition></bpmn:startEvent><bpmn:sequenceFlow id="tf" sourceRef="timerStart" targetRef="tick"/>` + jobNode("tick", "timer-tick") + `</bpmn:subProcess>`
	return wrapDefinitions(id, body)
}
func userTaskModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="review"/><bpmn:userTask id="review"><bpmn:extensionElements><zeebe:userTask/></bpmn:extensionElements></bpmn:userTask><bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="end"/><bpmn:endEvent id="end"/>`
	return wrapDefinitions(id, body)
}
func compensationModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="book"/>` + jobNode("book", "book") + `<bpmn:boundaryEvent id="bookComp" attachedToRef="book"><bpmn:compensateEventDefinition id="bookCompDef"/></bpmn:boundaryEvent><bpmn:serviceTask id="undo" name="Undo book" isForCompensation="true"><bpmn:extensionElements><zeebe:taskDefinition type="undo-book" retries="3"/></bpmn:extensionElements></bpmn:serviceTask><bpmn:sequenceFlow id="f2" sourceRef="book" targetRef="throw"/><bpmn:intermediateThrowEvent id="throw"><bpmn:compensateEventDefinition id="throwCompDef" activityRef="book"/></bpmn:intermediateThrowEvent><bpmn:sequenceFlow id="f3" sourceRef="throw" targetRef="end"/><bpmn:endEvent id="end"/><bpmn:association id="assoc" sourceRef="bookComp" targetRef="undo"/>`
	return wrapDefinitions(id, body)
}
