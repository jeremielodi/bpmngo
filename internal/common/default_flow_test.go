package common

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jeremielodi/bpmngo/internal/engine"
)

const defaultFlowXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <bpmn:exclusiveGateway id="gw" default="fDefault"/>
    <bpmn:sequenceFlow id="fBig" sourceRef="gw" targetRef="big">
      <bpmn:conditionExpression>= amount &gt; 100</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fDefault" sourceRef="gw" targetRef="small"/>
    <bpmn:userTask id="big"/><bpmn:userTask id="small"/>
    <bpmn:sequenceFlow id="f3" sourceRef="big" targetRef="e"/>
    <bpmn:sequenceFlow id="f4" sourceRef="small" targetRef="e"/>
    <bpmn:endEvent id="e"/>
  </bpmn:process></bpmn:definitions>`

func TestExclusiveGateway_DefaultFlow(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(defaultFlowXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		amount float64
		want   string
	}{
		{50, "small"}, // no condition true: the default flow
		{500, "big"},  // a condition true: the default flow is ignored
	}
	for _, c := range cases {
		got, err := ResolveNext(g.Nodes["gw"], map[string]interface{}{"amount": c.amount})
		if err != nil || got != c.want {
			t.Errorf("amount=%v: got %q, %v; want %q", c.amount, got, err, c.want)
		}
	}
}

// withExclusiveOrder runs f with EXCLUSIVE_GATEWAY_ORDER set to camunda
// (lastTrue) or bpmn.
func withExclusiveOrder(t *testing.T, lastTrue bool, f func()) {
	t.Helper()
	saved := exclusiveLastTrue
	exclusiveLastTrue = func() bool { return lastTrue }
	defer func() { exclusiveLastTrue = saved }()
	f()
}

// Two conditions true. camunda (default): the last sequenceFlow of the
// file, as Camunda 8.10.7 does; bpmn: the first, as BPMN 2.0 specifies.
// No condition true and no default: an error, in both.
func TestExclusiveGateway_TwoConditionsTrue(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(firstTrueXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct {
		name     string
		lastTrue bool
		both     string
	}{{"camunda", true, "huge"}, {"bpmn", false, "big"}} {
		withExclusiveOrder(t, mode.lastTrue, func() {
			cases := []struct {
				amount  float64
				want    string
				wantErr bool
			}{
				{5000, mode.both, false}, // both > 100 and > 1000 are true
				{500, "big", false},      // only > 100
				{50, "", true},           // none, and no default
			}
			for _, c := range cases {
				got, err := ResolveNext(g.Nodes["gw"], map[string]interface{}{"amount": c.amount})
				if (err != nil) != c.wantErr || got != c.want {
					t.Errorf("%s, amount=%v: got %q, %v; want %q (error %v)", mode.name, c.amount, got, err, c.want, c.wantErr)
				}
			}
		})
	}
}

// The gateway lists its <outgoing> flows: bpmn follows that list; camunda
// ignores it and takes the last true flow of the file (Camunda 8.10.7 took
// A with <outgoing> A,B,C and flows written C,B,A).
func TestExclusiveGateway_ExplicitOutgoingOrder(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(explicitOutgoingOrderXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]interface{}{"amount": 5000.0}
	withExclusiveOrder(t, false, func() {
		if got, err := ResolveNext(g.Nodes["gw"], vars); err != nil || got != "huge" {
			t.Errorf("bpmn: got %q, %v; want the first <outgoing> target %q", got, err, "huge")
		}
	})
	g2, err := engine.BuildGraphForProcess([]byte(outgoingAgainstFileOrderXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	withExclusiveOrder(t, true, func() {
		if got, err := ResolveNext(g2.Nodes["gw"], vars); err != nil || got != "big" {
			t.Errorf("camunda: got %q, %v; want the last flow of the file %q", got, err, "big")
		}
	})
	withExclusiveOrder(t, false, func() {
		if got, err := ResolveNext(g2.Nodes["gw"], vars); err != nil || got != "huge" {
			t.Errorf("bpmn: got %q, %v; want the first <outgoing> target %q", got, err, "huge")
		}
	})
}

// A graph stored before DocOrder was recorded: camunda falls back to its
// stored order, from the end.
func TestExclusiveGateway_GraphWithoutDocOrder(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(firstTrueXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	gw := *g.Nodes["gw"]
	gw.Outgoing = slices.Clone(gw.Outgoing)
	for i := range gw.Outgoing {
		gw.Outgoing[i].DocOrder = 0
	}
	withExclusiveOrder(t, true, func() {
		if got, err := ResolveNext(&gw, map[string]interface{}{"amount": 5000.0}); err != nil || got != "huge" {
			t.Errorf("got %q, %v; want %q", got, err, "huge")
		}
	})
}

// outgoing: huge then big; file: huge's flow then big's.
const outgoingAgainstFileOrderXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/><bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <bpmn:exclusiveGateway id="gw"><bpmn:outgoing>fHuge</bpmn:outgoing><bpmn:outgoing>fBig</bpmn:outgoing></bpmn:exclusiveGateway>
    <bpmn:sequenceFlow id="fHuge" sourceRef="gw" targetRef="huge"><bpmn:conditionExpression>= amount &gt; 1000</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fBig" sourceRef="gw" targetRef="big"><bpmn:conditionExpression>= amount &gt; 100</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:userTask id="big"/><bpmn:userTask id="huge"/>
  </bpmn:process></bpmn:definitions>`

const explicitOutgoingOrderXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/><bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <bpmn:exclusiveGateway id="gw"><bpmn:outgoing>fHuge</bpmn:outgoing><bpmn:outgoing>fBig</bpmn:outgoing></bpmn:exclusiveGateway>
    <bpmn:sequenceFlow id="fBig" sourceRef="gw" targetRef="big"><bpmn:conditionExpression>= amount &gt; 100</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fHuge" sourceRef="gw" targetRef="huge"><bpmn:conditionExpression>= amount &gt; 1000</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:userTask id="big"/><bpmn:userTask id="huge"/>
  </bpmn:process></bpmn:definitions>`

const firstTrueXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <bpmn:exclusiveGateway id="gw"/>
    <bpmn:sequenceFlow id="fBig" sourceRef="gw" targetRef="big">
      <bpmn:conditionExpression>= amount &gt; 100</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fHuge" sourceRef="gw" targetRef="huge">
      <bpmn:conditionExpression>= amount &gt; 1000</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:userTask id="big"/><bpmn:userTask id="huge"/>
    <bpmn:sequenceFlow id="f3" sourceRef="big" targetRef="e"/>
    <bpmn:sequenceFlow id="f4" sourceRef="huge" targetRef="e"/>
    <bpmn:endEvent id="e"/>
  </bpmn:process></bpmn:definitions>`

const inclusiveDefaultXML = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <bpmn:inclusiveGateway id="gw" default="fNone"/>
    <bpmn:sequenceFlow id="fA" sourceRef="gw" targetRef="a">
      <bpmn:conditionExpression>= wantA</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="fNone" sourceRef="gw" targetRef="none"/>
    <bpmn:sequenceFlow id="fB" sourceRef="gw" targetRef="b">
      <bpmn:conditionExpression>= wantB</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:userTask id="a"/><bpmn:userTask id="b"/><bpmn:userTask id="none"/>
    <bpmn:endEvent id="e"/>
  </bpmn:process></bpmn:definitions>`

func TestInclusiveGateway_DefaultFlow(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(inclusiveDefaultXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes["gw"].DefaultFlow != "fNone" {
		t.Fatalf("default attribute not parsed: %q", g.Nodes["gw"].DefaultFlow)
	}
	cases := []struct {
		wantA, wantB bool
		want         string
	}{
		{false, false, "none"}, // nothing true: the default alone
		{true, false, "a"},     // one true: that branch, not the default
		{true, true, "a,b"},    // two true: both, still not the default
	}
	for _, c := range cases {
		flows, err := ResolveInclusiveBranches(g.Nodes["gw"], map[string]interface{}{"wantA": c.wantA, "wantB": c.wantB})
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		var targets []string
		for _, f := range flows {
			targets = append(targets, f.TargetRef)
		}
		if got := strings.Join(targets, ","); got != c.want {
			t.Errorf("wantA=%v wantB=%v: got %q, want %q", c.wantA, c.wantB, got, c.want)
		}
	}
}

func TestInclusiveGateway_NothingTrueNoDefault(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(strings.Replace(inclusiveDefaultXML, ` default="fNone"`, "", 1)), "p")
	if err != nil {
		t.Fatal(err)
	}
	// Without the default attribute fNone is an unconditioned flow: true.
	flows, err := ResolveInclusiveBranches(g.Nodes["gw"], map[string]interface{}{"wantA": true, "wantB": false})
	if err != nil || len(flows) != 2 {
		t.Fatalf("got %v, %v; want fA and fNone", flows, err)
	}
	g.Nodes["gw"].Outgoing = g.Nodes["gw"].Outgoing[:1] // only fA left
	if _, err := ResolveInclusiveBranches(g.Nodes["gw"], map[string]interface{}{"wantA": false}); err == nil {
		t.Fatal("no branch true and no default: want an error (incident)")
	}
}

// The graph is stored as JSON at deploy time: the default must survive it.
func TestDefaultFlow_SurvivesGraphJSON(t *testing.T) {
	g, err := engine.BuildGraphForProcess([]byte(defaultFlowXML), "p")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var back engine.ProcessGraph
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Nodes["gw"].DefaultFlow != "fDefault" {
		t.Fatalf("DefaultFlow lost in JSON: %q", back.Nodes["gw"].DefaultFlow)
	}
}
