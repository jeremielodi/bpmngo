package dmn

import (
	"reflect"
	"strings"
	"testing"
)

// table builds a one-input decision table on "amount" with one output per
// name in outputs; each rule is its input entry followed by its output
// entries.
func table(t *testing.T, hitPolicy, aggregation, outputValues string, outputs []string, rules ...[]string) *DecisionTable {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/"><decision id="d"><decisionTable id="t"`)
	if hitPolicy != "" {
		b.WriteString(` hitPolicy="` + hitPolicy + `"`)
	}
	if aggregation != "" {
		b.WriteString(` aggregation="` + aggregation + `"`)
	}
	b.WriteString(`><input id="i"><inputExpression><text>amount</text></inputExpression></input>`)
	for i, o := range outputs {
		b.WriteString(`<output id="o` + o + `" name="` + o + `">`)
		if i == 0 && outputValues != "" {
			b.WriteString(`<outputValues><text>` + outputValues + `</text></outputValues>`)
		}
		b.WriteString(`</output>`)
	}
	for i, r := range rules {
		b.WriteString(`<rule id="r` + string(rune('1'+i)) + `"><inputEntry><text>` + r[0] + `</text></inputEntry>`)
		for _, out := range r[1:] {
			b.WriteString(`<outputEntry><text>` + out + `</text></outputEntry>`)
		}
		b.WriteString(`</rule>`)
	}
	b.WriteString(`</decisionTable></decision></definitions>`)
	defs, err := ParseDMN([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return defs.Decisions[0].DecisionTable
}

func eval(t *testing.T, tbl *DecisionTable, amount float64) (interface{}, error) {
	t.Helper()
	return EvaluateDecisionTable(tbl, map[string]interface{}{"amount": amount})
}

func TestUnique(t *testing.T) {
	tbl := table(t, "UNIQUE", "", "", []string{"r"},
		[]string{"&lt; 100", `"small"`},
		[]string{"[100..500]", `"medium"`},
		[]string{"&gt;= 400", `"large"`}) // overlaps [400..500]
	if got, err := eval(t, tbl, 50); err != nil || got != "small" {
		t.Errorf("one match: got %v, %v", got, err)
	}
	if got, err := eval(t, tbl, 450); err == nil {
		t.Errorf("two matches under UNIQUE must be an error, got %v", got)
	}
	// No hitPolicy attribute means UNIQUE.
	if got, err := eval(t, table(t, "", "", "", []string{"r"}, []string{"-", `"a"`}, []string{"-", `"b"`}), 1); err == nil {
		t.Errorf("default hit policy is UNIQUE, got %v", got)
	}
}

func TestNoMatch(t *testing.T) {
	tbl := table(t, "UNIQUE", "", "", []string{"r"}, []string{"&lt; 0", `"neg"`})
	if got, err := eval(t, tbl, 5); err != nil || got != nil {
		t.Errorf("no match: got %v, %v; want nil", got, err)
	}
	collect := table(t, "COLLECT", "", "", []string{"r"}, []string{"&lt; 0", "1"})
	if got, err := eval(t, collect, 5); err != nil || len(got.([]interface{})) != 0 {
		t.Errorf("COLLECT no match: got %v, %v; want []", got, err)
	}
}

func TestFirstAndAny(t *testing.T) {
	first := table(t, "FIRST", "", "", []string{"r"}, []string{"&gt; 10", `"a"`}, []string{"&gt; 5", `"b"`})
	if got, _ := eval(t, first, 20); got != "a" {
		t.Errorf("FIRST: got %v", got)
	}
	same := table(t, "ANY", "", "", []string{"r"}, []string{"&gt; 10", `"x"`}, []string{"&gt; 5", `"x"`})
	if got, err := eval(t, same, 20); err != nil || got != "x" {
		t.Errorf("ANY with equal outputs: got %v, %v", got, err)
	}
	differ := table(t, "ANY", "", "", []string{"r"}, []string{"&gt; 10", `"x"`}, []string{"&gt; 5", `"y"`})
	if got, err := eval(t, differ, 20); err == nil {
		t.Errorf("ANY with different outputs must be an error, got %v", got)
	}
}

func TestRuleOrderPriorityOutputOrder(t *testing.T) {
	rules := [][]string{{"&gt; 0", `"low"`}, {"&gt; 10", `"high"`}, {"&gt; 5", `"mid"`}}
	ruleOrder := table(t, "RULE ORDER", "", "", []string{"r"}, rules...)
	if got, _ := eval(t, ruleOrder, 20); !reflect.DeepEqual(got, []interface{}{"low", "high", "mid"}) {
		t.Errorf("RULE ORDER: got %v", got)
	}
	values := `"high","mid","low"`
	priority := table(t, "PRIORITY", "", values, []string{"r"}, rules...)
	if got, err := eval(t, priority, 20); err != nil || got != "high" {
		t.Errorf("PRIORITY: got %v, %v", got, err)
	}
	if got, _ := eval(t, priority, 7); got != "mid" {
		t.Errorf("PRIORITY among low/mid: got %v", got)
	}
	outputOrder := table(t, "OUTPUT ORDER", "", values, []string{"r"}, rules...)
	if got, _ := eval(t, outputOrder, 20); !reflect.DeepEqual(got, []interface{}{"high", "mid", "low"}) {
		t.Errorf("OUTPUT ORDER: got %v", got)
	}
	if got, err := eval(t, table(t, "PRIORITY", "", "", []string{"r"}, rules...), 20); err == nil {
		t.Errorf("PRIORITY without outputValues must be an error, got %v", got)
	}
}

func TestCollectAggregations(t *testing.T) {
	rules := [][]string{{"&gt; 0", "10"}, {"&gt; 5", "3"}, {"&gt; 100", "1000"}, {"&gt; 1", "7"}}
	cases := map[string]interface{}{
		"":      []interface{}{10.0, 3.0, 7.0},
		"SUM":   20.0,
		"COUNT": 3.0,
		"MIN":   3.0,
		"MAX":   10.0,
	}
	for agg, want := range cases {
		got, err := eval(t, table(t, "COLLECT", agg, "", []string{"r"}, rules...), 20)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("COLLECT %q: got %v, %v; want %v", agg, got, err, want)
		}
	}
	if got, err := eval(t, table(t, "COLLECT", "SUM", "", []string{"r"}, rules...), -1); err != nil || got != nil {
		t.Errorf("COLLECT SUM with no match: got %v, %v; want nil", got, err)
	}
	if got, err := eval(t, table(t, "COLLECT", "SUM", "", []string{"r"}, []string{"-", `"text"`}), 1); err == nil {
		t.Errorf("COLLECT SUM over strings must be an error, got %v", got)
	}
}

func TestMultipleOutputsAndWildcard(t *testing.T) {
	tbl := table(t, "UNIQUE", "", "", []string{"approved", "rate"},
		[]string{"&lt; 1000", "true", "0.1"},
		[]string{"-", "false", "0"}) // "-" matches anything...
	// ...so 500 matches both rules under UNIQUE.
	if _, err := eval(t, tbl, 500); err == nil {
		t.Error("overlapping wildcard under UNIQUE must be an error")
	}
	got, err := eval(t, tbl, 5000)
	want := map[string]interface{}{"approved": false, "rate": 0.0}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("two outputs: got %v, %v; want %v", got, err, want)
	}
}

func TestUnaryTests(t *testing.T) {
	cases := []struct {
		test  string
		value interface{}
		want  bool
	}{
		{"-", 1.0, true},
		{"", "x", true},
		{"&lt; 10", 9.0, true},
		{"&lt;= 10", 10.0, true},
		{"[1..5]", 5.0, true},
		{"(1..5)", 1.0, false},
		{`"gold","platinum"`, "platinum", true},
		{`"gold","platinum"`, "silver", false},
		{"42", 42.0, true},
	}
	for _, c := range cases {
		test := strings.NewReplacer("&lt;", "<", "&gt;", ">").Replace(c.test)
		got, err := matchesUnaryTest(test, c.value)
		if err != nil || got != c.want {
			t.Errorf("%q against %v: got %v, %v; want %v", test, c.value, got, err, c.want)
		}
	}
}
