package engine

import "testing"

const errorCatchersXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
  <bpmn:error id="ErrCard" errorCode="CARD_DECLINED"/>
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="pay"/>
    <bpmn:serviceTask id="pay"/>
    <bpmn:sequenceFlow id="f2" sourceRef="pay" targetRef="e"/>
    <bpmn:endEvent id="e"/>
    <bpmn:boundaryEvent id="onCard" attachedToRef="pay"><bpmn:errorEventDefinition errorRef="ErrCard"/></bpmn:boundaryEvent>
    <bpmn:boundaryEvent id="onAny" attachedToRef="pay"><bpmn:errorEventDefinition/></bpmn:boundaryEvent>
    <bpmn:sequenceFlow id="f3" sourceRef="onCard" targetRef="e"/>
    <bpmn:sequenceFlow id="f4" sourceRef="onAny" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestFindErrorCatcher_CatchAll(t *testing.T) {
	g, err := BuildGraphForProcess([]byte(errorCatchersXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"CARD_DECLINED": "onCard", // the exact code wins over the catch-all
		"TIMEOUT":       "onAny",  // any other code: the catch-all
	}
	for code, want := range cases {
		if c, _ := g.FindErrorCatcher("pay", code); c == nil || c.ID != want {
			t.Errorf("error %s: caught by %v, want %s", code, c, want)
		}
	}
	// Without the catch-all, an unknown code is not caught.
	delete(g.Nodes, "onAny")
	if c, _ := g.FindErrorCatcher("pay", "TIMEOUT"); c != nil {
		t.Errorf("no catch-all: TIMEOUT caught by %s", c.ID)
	}
}
