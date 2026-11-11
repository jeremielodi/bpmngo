package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
)

var generated = regexp.MustCompile(`(?i)(key|date|time|cursor)$`)

func normalize(value any, field string) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		allObjects := true
		for i := range v {
			out[i] = normalize(v[i], field)
			if _, ok := out[i].(map[string]any); !ok {
				allObjects = false
			}
		}
		if allObjects {
			sort.Slice(out, func(i, j int) bool {
				a, _ := json.Marshal(out[i])
				b, _ := json.Marshal(out[j])
				return string(a) < string(b)
			})
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = normalize(item, k)
		}
		return out
	default:
		if generated.MatchString(field) || field == "processDefinitionVersion" || field == "redelivery_ms" {
			return "<generated>"
		}
		return value
	}
}

func load(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var value map[string]any
	if err := json.Unmarshal(b, &value); err != nil {
		panic(err)
	}
	return value
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: compare_behavior CAMUNDA.json BPMNGO.json")
		os.Exit(2)
	}
	reference := load(os.Args[1])["scenarios"].([]any)
	candidate := load(os.Args[2])["scenarios"].([]any)
	if len(reference) != len(candidate) {
		fmt.Printf("scenario count differs: %d vs %d\n", len(reference), len(candidate))
		os.Exit(1)
	}
	identical := 0
	for i := range reference {
		a := reference[i].(map[string]any)
		b := candidate[i].(map[string]any)
		an, _ := json.Marshal(normalize(a["observations"], ""))
		bn, _ := json.Marshal(normalize(b["observations"], ""))
		if a["name"] == b["name"] && string(an) == string(bn) {
			identical++
			continue
		}
		fmt.Printf("DIFF %d: %v vs %v\n", i+1, a["name"], b["name"])
	}
	fmt.Printf("IDENTICAL_NORMALIZED %d/%d\n", identical, len(reference))
}
