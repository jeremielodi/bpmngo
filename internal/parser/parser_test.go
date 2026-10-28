package parser_test

import (
	"testing"

	"github.com/jeremielodi/bpmngo/internal/engine"
	"github.com/jeremielodi/bpmngo/internal/parser"
)

const zeebeXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="d">
  <bpmn:process id="z" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="review"/>
    <bpmn:userTask id="review" name="Review">
      <bpmn:extensionElements>
        <zeebe:assignmentDefinition assignee="= owner" candidateGroups="sales,finance"/>
        <zeebe:formDefinition formKey="camunda-forms:bpmn:review-form"/>
        <zeebe:priorityDefinition priority="80"/>
        <zeebe:ioMapping><zeebe:input source="= order.id" target="orderId"/></zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:sequenceFlow id="f2" sourceRef="review" targetRef="charge"/>
    <bpmn:serviceTask id="charge">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="charge-card" retries="5"/>
        <zeebe:taskHeaders><zeebe:header key="currency" value="EUR"/></zeebe:taskHeaders>
        <zeebe:ioMapping>
          <zeebe:input source="= amount" target="total"/>
          <zeebe:output source="= receipt" target="receiptId"/>
        </zeebe:ioMapping>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:sequenceFlow id="f3" sourceRef="charge" targetRef="e"/>
    <bpmn:endEvent id="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestZeebeExtensions(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(zeebeXML), "z")
	if err != nil {
		t.Fatal(err)
	}
	review := g.Nodes["review"]
	if review.AssigneeExpr == nil || *review.AssigneeExpr != "= owner" {
		t.Errorf("assignee: %v", review.AssigneeExpr)
	}
	if review.CandidateGroupExpr == nil || *review.CandidateGroupExpr != "sales,finance" {
		t.Errorf("candidateGroups: %v", review.CandidateGroupExpr)
	}
	if review.FormKey != "camunda-forms:bpmn:review-form" {
		t.Errorf("formKey: %q", review.FormKey)
	}
	if review.PriorityExpr == nil || *review.PriorityExpr != "80" {
		t.Errorf("priority: %v", review.PriorityExpr)
	}
	if len(review.InputMappings) != 1 || review.InputMappings[0].Target != "orderId" {
		t.Errorf("user task input mapping: %+v", review.InputMappings)
	}

	charge := g.Nodes["charge"]
	if charge.JobType == nil || *charge.JobType != "charge-card" {
		t.Errorf("job type: %v", charge.JobType)
	}
	if charge.TaskHeaders["currency"] != "EUR" {
		t.Errorf("task headers: %v", charge.TaskHeaders)
	}
	if len(charge.InputMappings) != 1 || charge.InputMappings[0].Source != "= amount" || charge.InputMappings[0].Target != "total" {
		t.Errorf("input mapping: %+v", charge.InputMappings)
	}
	if len(charge.OutputMappings) != 1 || charge.OutputMappings[0].Target != "receiptId" {
		t.Errorf("output mapping: %+v", charge.OutputMappings)
	}
}

// Camunda 7 attributes, with the namespace as Camunda Modeler writes it.
const camunda7XML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:camunda="http://camunda.org/schema/1.0/bpmn" id="d">
  <bpmn:process id="c7" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="approve"/>
    <bpmn:userTask id="approve" camunda:assignee="${initiator}" camunda:candidateGroups="managers" camunda:formKey="embedded:app:forms/approve.html"/>
    <bpmn:sequenceFlow id="f2" sourceRef="approve" targetRef="notify"/>
    <bpmn:serviceTask id="notify" camunda:type="external" camunda:topic="send-mail"/>
    <bpmn:sequenceFlow id="f3" sourceRef="notify" targetRef="e"/>
    <bpmn:endEvent id="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestCamunda7Attributes(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(camunda7XML), "c7")
	if err != nil {
		t.Fatal(err)
	}
	approve := g.Nodes["approve"]
	if approve.AssigneeExpr == nil || *approve.AssigneeExpr != "${initiator}" {
		t.Errorf("assignee: %v", approve.AssigneeExpr)
	}
	if approve.CandidateGroupExpr == nil || *approve.CandidateGroupExpr != "managers" {
		t.Errorf("candidateGroups: %v", approve.CandidateGroupExpr)
	}
	if approve.FormKey != "embedded:app:forms/approve.html" {
		t.Errorf("formKey: %q", approve.FormKey)
	}
	if n := g.Nodes["notify"]; n.JobType == nil || *n.JobType != "send-mail" {
		t.Errorf("external task topic: %v", n.JobType)
	}
}

func TestMalformedBPMN(t *testing.T) {
	for name, xml := range map[string]string{
		"truncated":   `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"><bpmn:process id="p">`,
		"not xml":     `this is not BPMN`,
		"unknown key": zeebeXML,
	} {
		key := "p"
		if name == "unknown key" {
			key = "no-such-process"
		}
		if _, err := engine.BuildGraphForProcess([]byte(xml), key); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := parser.ParseBPMN([]byte(`<definitions><process id="p"><startEvent`)); err == nil {
		t.Error("ParseBPMN on truncated XML: want an error")
	}
}

func TestDetectEngine(t *testing.T) {
	cases := map[string]parser.EngineType{
		camunda7XML: parser.EngineCamunda7,
		`<definitions xmlns:camunda="http://camunda.org/schema/1.0/BPMN"/>`: parser.EngineCamunda7,
		zeebeXML: parser.EngineCamunda8,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"/>`: parser.EngineUnknown,
	}
	for xml, want := range cases {
		if got := parser.DetectEngine([]byte(xml)); got != want {
			t.Errorf("DetectEngine(%.60q…) = %s, want %s", xml, got, want)
		}
	}
}
