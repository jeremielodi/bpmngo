package main

import (
	"fmt"
	"strings"
	"time"

	pb "github.com/jeremielodi/bpmngo/internal/zeebegrpc/pb"
	"google.golang.org/grpc/status"
)

func eventAndJobScenarios(start int) []scenario {
	var out []scenario
	next := func() string { start++; return fmt.Sprintf("B%02d", start) }

	id := next()
	out = append(out, scenario{name: "boundary-timer-interrupting", category: "boundary", processID: id, model: boundaryTimerModel(id, true), run: observeOne("timer-caught", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "boundary-timer-non-interrupting", category: "boundary", processID: id, model: boundaryTimerModel(id, false), run: observeTwoTypes("main-work", "timer-caught", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "boundary-message", category: "boundary", processID: id, variables: `{"orderId":"BOUNDARY"}`, model: boundaryMessageModel(id), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			main, mainErr := r.activateFor("main-work", pi, 2*time.Second, 1)
			if mainErr != nil {
				return nil, mainErr
			}
			time.Sleep(time.Second)
			_, publication, err := r.restJSON("POST", "/v2/messages/publication?physicalTenantId=default", map[string]any{"name": "BoundaryMessage" + sid, "correlationKey": "BOUNDARY", "timeToLive": 5000, "messageId": fmt.Sprintf("m-%d", pi), "variables": map[string]any{"fromMessage": true}})
			if err != nil {
				return nil, err
			}
			caught, caughtErr := r.activateFor("message-caught", pi, 2*time.Second, 1)
			obs := map[string]any{"main": jobObservation(main[0]), "caught_count": len(caught), "publication": publication}
			if len(caught) > 0 {
				obs["caught"] = jobObservation(caught[0])
			}
			return obs, caughtErr
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "boundary-signal-non-interrupting", category: "boundary", processID: id, model: boundarySignalModel(id, false), run: func(sid string) func(*runner, int64) (map[string]any, error) {
		return func(r *runner, pi int64) (map[string]any, error) {
			main, mainErr := r.activateFor("main-work", pi, 2*time.Second, 1)
			if mainErr != nil {
				return nil, mainErr
			}
			time.Sleep(time.Second)
			_, broadcast, err := r.restJSON("POST", "/v2/signals/broadcast?physicalTenantId=default", map[string]any{"signalName": "BoundarySignal" + sid, "variables": map[string]any{"fromSignal": true}})
			if err != nil {
				return nil, err
			}
			caught, caughtErr := r.activateFor("signal-caught", pi, 2*time.Second, 1)
			obs := map[string]any{"main": jobObservation(main[0]), "caught_count": len(caught), "broadcast": broadcast}
			if len(caught) > 0 {
				obs["caught"] = jobObservation(caught[0])
			}
			return obs, caughtErr
		}
	}(id)})
	id = next()
	out = append(out, scenario{name: "boundary-error-exact", category: "boundary", processID: id, model: boundaryErrorModel(id, "E_EXACT", false), run: throwAndObserve("pay", "E_EXACT", "error-caught")})
	id = next()
	out = append(out, scenario{name: "boundary-error-catch-all", category: "boundary", processID: id, model: boundaryErrorModel(id, "", true), run: throwAndObserve("pay", "ANY_CODE", "error-caught")})
	id = next()
	out = append(out, scenario{name: "boundary-escalation", category: "boundary", processID: id, model: escalationModel(id), run: observeOne("escalation-caught", 2*time.Second)})

	id = next()
	out = append(out, scenario{name: "job-fail-retries-remaining", category: "jobs", processID: id, model: singleJobModel(id, "fail-job", ""), run: func(r *runner, pi int64) (map[string]any, error) {
		jobs, err := r.activateFor("fail-job", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		ctx, cancel := r.ctx(5 * time.Second)
		_, err = r.client.FailJob(ctx, &pb.FailJobRequest{JobKey: jobs[0].GetKey(), Retries: 2, ErrorMessage: "transient", RetryBackOff: 0})
		cancel()
		if err != nil {
			return nil, err
		}
		again, err := r.activateFor("fail-job", pi, 2*time.Second, 1)
		obs := map[string]any{"first": jobObservation(jobs[0])}
		if len(again) > 0 {
			obs["second"] = jobObservation(again[0])
		}
		return obs, err
	}})
	id = next()
	out = append(out, scenario{name: "job-fail-no-retries-incident", category: "jobs", processID: id, model: singleJobModel(id, "incident-job", ""), run: func(r *runner, pi int64) (map[string]any, error) {
		jobs, err := r.activateFor("incident-job", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		ctx, cancel := r.ctx(5 * time.Second)
		_, err = r.client.FailJob(ctx, &pb.FailJobRequest{JobKey: jobs[0].GetKey(), Retries: 0, ErrorMessage: "permanent"})
		cancel()
		if err != nil {
			return nil, err
		}
		return r.waitIncident(pi, 4*time.Second)
	}})
	id = next()
	out = append(out, scenario{name: "job-fail-backoff", category: "jobs", processID: id, model: singleJobModel(id, "backoff-job", ""), run: func(r *runner, pi int64) (map[string]any, error) {
		jobs, err := r.activateFor("backoff-job", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		ctx, cancel := r.ctx(5 * time.Second)
		_, err = r.client.FailJob(ctx, &pb.FailJobRequest{JobKey: jobs[0].GetKey(), Retries: 2, ErrorMessage: "later", RetryBackOff: 700})
		cancel()
		if err != nil {
			return nil, err
		}
		t := time.Now()
		early, _ := r.activateFor("backoff-job", pi, 250*time.Millisecond, 1)
		late, lateErr := r.activateFor("backoff-job", pi, 2*time.Second, 1)
		obs := map[string]any{"early_count": len(early), "redelivery_ms": time.Since(t).Milliseconds()}
		if len(late) > 0 {
			obs["job"] = jobObservation(late[0])
		}
		return obs, lateErr
	}})
	id = next()
	out = append(out, scenario{name: "fail-job-variables", category: "jobs", processID: id, model: singleJobModel(id, "fail-vars", ""), run: func(r *runner, pi int64) (map[string]any, error) {
		jobs, err := r.activateFor("fail-vars", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		ctx, cancel := r.ctx(5 * time.Second)
		_, err = r.client.FailJob(ctx, &pb.FailJobRequest{JobKey: jobs[0].GetKey(), Retries: 2, ErrorMessage: "with vars", Variables: `{"failureReason":"bank"}`})
		cancel()
		if err != nil {
			return nil, err
		}
		again, err := r.activateFor("fail-vars", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		return map[string]any{"redelivered": jobObservation(again[0])}, nil
	}})
	id = next()
	out = append(out, scenario{name: "throw-error-variables", category: "jobs", processID: id, model: boundaryErrorModel(id, "BUSINESS", false), run: throwAndObserve("pay", "BUSINESS", "error-caught")})
	id = next()
	out = append(out, scenario{name: "job-custom-headers", category: "jobs", processID: id, variables: `{"amount":7}`, model: singleJobModel(id, "header-job", `<zeebe:taskHeaders><zeebe:header key="currency" value="EUR"/><zeebe:header key="source" value="fair-bench"/></zeebe:taskHeaders>`), run: observeOne("header-job", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "job-input-mapping", category: "jobs", processID: id, variables: `{"amount":7,"secret":"outer"}`, model: singleJobModel(id, "input-job", `<zeebe:ioMapping><zeebe:input source="= amount * 2" target="double"/></zeebe:ioMapping>`), run: observeOne("input-job", 2*time.Second)})
	id = next()
	out = append(out, scenario{name: "job-output-mapping", category: "jobs", processID: id, model: outputMappingModel(id), run: func(r *runner, pi int64) (map[string]any, error) {
		first, err := r.activateFor("mapped-first", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		ctx, cancel := r.ctx(5 * time.Second)
		_, err = r.client.CompleteJob(ctx, &pb.CompleteJobRequest{JobKey: first[0].GetKey(), Variables: `{"raw":21,"leak":"no"}`})
		cancel()
		if err != nil {
			return nil, err
		}
		second, err := r.activateFor("mapped-after", pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		return map[string]any{"after": jobObservation(second[0])}, nil
	}})

	return append(out, advancedScenarios(start)...)
}

func observeOne(jobType string, timeout time.Duration) func(*runner, int64) (map[string]any, error) {
	return func(r *runner, pi int64) (map[string]any, error) {
		jobs, err := r.activateFor(jobType, pi, timeout, 10)
		obs := map[string]any{"count": len(jobs)}
		if len(jobs) > 0 {
			var js []map[string]any
			for _, j := range jobs {
				js = append(js, jobObservation(j))
			}
			obs["jobs"] = js
		}
		return obs, err
	}
}
func observeTwoTypes(a, b string, timeout time.Duration) func(*runner, int64) (map[string]any, error) {
	return func(r *runner, pi int64) (map[string]any, error) {
		ja, ea := r.activateFor(a, pi, timeout, 10)
		jb, eb := r.activateFor(b, pi, timeout, 10)
		obs := map[string]any{"first_count": len(ja), "second_count": len(jb)}
		if len(ja) > 0 {
			obs["first"] = jobObservation(ja[0])
		}
		if len(jb) > 0 {
			obs["second"] = jobObservation(jb[0])
		}
		if ea != nil {
			return obs, ea
		}
		return obs, eb
	}
}
func throwAndObserve(source, errorCode, target string) func(*runner, int64) (map[string]any, error) {
	return func(r *runner, pi int64) (map[string]any, error) {
		jobs, err := r.activateFor(source, pi, 2*time.Second, 1)
		if err != nil {
			return nil, err
		}
		ctx, cancel := r.ctx(5 * time.Second)
		_, err = r.client.ThrowError(ctx, &pb.ThrowErrorRequest{JobKey: jobs[0].GetKey(), ErrorCode: errorCode, ErrorMessage: "declined", Variables: `{"declinedBy":"bank"}`})
		cancel()
		if err != nil {
			return map[string]any{"throw_code": grpcCode(err)}, err
		}
		caught, err := r.activateFor(target, pi, 2*time.Second, 1)
		obs := map[string]any{"throw_code": "OK"}
		if len(caught) > 0 {
			obs["caught"] = jobObservation(caught[0])
		}
		return obs, err
	}
}

func singleJobModel(id, typ, extensions string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/><bpmn:serviceTask id="work"><bpmn:extensionElements><zeebe:taskDefinition type="` + typ + `" retries="3"/>` + extensions + `</bpmn:extensionElements></bpmn:serviceTask><bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/><bpmn:endEvent id="end"/>`
	return wrapDefinitions(id, body)
}
func boundaryTimerModel(id string, interrupt bool) string {
	cancel := "false"
	if interrupt {
		cancel = "true"
	}
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>` + jobNode("work", "main-work") + `<bpmn:sequenceFlow id="f2" sourceRef="work" targetRef="end"/><bpmn:endEvent id="end"/><bpmn:boundaryEvent id="timer" attachedToRef="work" cancelActivity="` + cancel + `"><bpmn:timerEventDefinition><bpmn:timeDuration>PT0.2S</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent><bpmn:sequenceFlow id="ft" sourceRef="timer" targetRef="caught"/>` + jobNode("caught", "timer-caught")
	return wrapDefinitions(id, body)
}
func boundaryMessageModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>` + jobNode("work", "main-work") + `<bpmn:boundaryEvent id="msgBoundary" attachedToRef="work"><bpmn:messageEventDefinition messageRef="msg_` + id + `"/></bpmn:boundaryEvent><bpmn:sequenceFlow id="fm" sourceRef="msgBoundary" targetRef="caught"/>` + jobNode("caught", "message-caught")
	return wrapCustom(id, `<bpmn:message id="msg_`+id+`" name="BoundaryMessage`+id+`"><bpmn:extensionElements><zeebe:subscription correlationKey="= orderId"/></bpmn:extensionElements></bpmn:message>`, body)
}
func boundarySignalModel(id string, interrupt bool) string {
	cancel := "false"
	if interrupt {
		cancel = "true"
	}
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="work"/>` + jobNode("work", "main-work") + `<bpmn:boundaryEvent id="sigBoundary" attachedToRef="work" cancelActivity="` + cancel + `"><bpmn:signalEventDefinition signalRef="sig_` + id + `"/></bpmn:boundaryEvent><bpmn:sequenceFlow id="fs" sourceRef="sigBoundary" targetRef="caught"/>` + jobNode("caught", "signal-caught")
	return wrapCustom(id, `<bpmn:signal id="sig_`+id+`" name="BoundarySignal`+id+`"/>`, body)
}
func boundaryErrorModel(id, code string, catchAll bool) string {
	ref := ` errorRef="err_` + id + `"`
	extra := `<bpmn:error id="err_` + id + `" errorCode="` + code + `"/>`
	if catchAll {
		ref = ""
		extra = ""
	}
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pay"/>` + jobNode("pay", "pay") + `<bpmn:boundaryEvent id="errBoundary" attachedToRef="pay"><bpmn:errorEventDefinition` + ref + `/></bpmn:boundaryEvent><bpmn:sequenceFlow id="fe" sourceRef="errBoundary" targetRef="caught"/>` + jobNode("caught", "error-caught")
	return wrapCustom(id, extra, body)
}
func escalationModel(id string) string {
	extra := `<bpmn:escalation id="esc_` + id + `" escalationCode="ESC"/>`
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="sub"/><bpmn:subProcess id="sub"><bpmn:startEvent id="subStart"/><bpmn:sequenceFlow id="sf" sourceRef="subStart" targetRef="escEnd"/><bpmn:endEvent id="escEnd"><bpmn:escalationEventDefinition escalationRef="esc_` + id + `"/></bpmn:endEvent></bpmn:subProcess><bpmn:boundaryEvent id="escBoundary" attachedToRef="sub"><bpmn:escalationEventDefinition escalationRef="esc_` + id + `"/></bpmn:boundaryEvent><bpmn:sequenceFlow id="fe" sourceRef="escBoundary" targetRef="caught"/>` + jobNode("caught", "escalation-caught")
	return wrapCustom(id, extra, body)
}
func outputMappingModel(id string) string {
	body := `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="first"/><bpmn:serviceTask id="first"><bpmn:extensionElements><zeebe:taskDefinition type="mapped-first"/><zeebe:ioMapping><zeebe:output source="= raw * 2" target="mapped"/></zeebe:ioMapping></bpmn:extensionElements></bpmn:serviceTask><bpmn:sequenceFlow id="f2" sourceRef="first" targetRef="after"/>` + jobNode("after", "mapped-after")
	return wrapDefinitions(id, body)
}
func wrapCustom(id, extra, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="D_` + id + `" targetNamespace="http://fair-benchmark/` + id + `">` + extra + `<bpmn:process id="` + id + `" isExecutable="true">` + body + `</bpmn:process></bpmn:definitions>`
}

func (r *runner) waitIncident(pi int64, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	var last map[string]any
	for time.Now().Before(deadline) {
		code, res, err := r.restJSON("POST", "/v2/incidents/search", map[string]any{"filter": map[string]any{"processInstanceKey": fmt.Sprint(pi)}})
		last = map[string]any{"http_status": code, "response": res}
		if err == nil {
			if items, ok := res["items"].([]any); ok && len(items) > 0 {
				return last, nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return last, status.Error(4, "incident not observed")
}

// Keep strings imported even when an engine-specific build eliminates paths.
var _ = strings.Builder{}
