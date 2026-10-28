package service

import (
	"reflect"
	"testing"
)

func TestExtractProcessKeys_SkipsNonExecutable(t *testing.T) {
	xml := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="orders" isExecutable="true"/>
	  <process id="customer" isExecutable="false"/>
	  <process id="legacy"/>
	</definitions>`
	keys, err := extractProcessKeys([]byte(xml))
	// "legacy" has no isExecutable attribute: kept, as before.
	if err != nil || !reflect.DeepEqual(keys, []string{"orders", "legacy"}) {
		t.Fatalf("got %v, %v", keys, err)
	}
	if _, err := extractProcessKeys([]byte(`<definitions><process id="x" isExecutable="false"/></definitions>`)); err == nil {
		t.Fatal("only non-executable processes: want an error")
	}
	if _, err := extractProcessKeys([]byte(`<definitions><process`)); err == nil {
		t.Fatal("truncated XML: want an error")
	}
}
