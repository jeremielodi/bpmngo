package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type resources struct {
	DurationSeconds         float64            `json:"duration_seconds"`
	CPUSeconds              float64            `json:"cpu_seconds"`
	CPUSecondsByContainer   map[string]float64 `json:"cpu_seconds_by_container"`
	CPUAveragePercentExact  float64            `json:"cpu_average_percent_exact"`
	CPUAveragePercentSample float64            `json:"cpu_average_percent_sampled"`
	CPUPeakPercentSample    float64            `json:"cpu_peak_percent_sampled"`
	MemoryIdleBytes         float64            `json:"memory_idle_bytes_sampled"`
	MemoryPeakBytes         float64            `json:"memory_peak_bytes_sampled"`
	SampleCount             int                `json:"sample_count"`
	CPUAverageByContainer   map[string]float64 `json:"cpu_average_percent_by_container_sampled"`
}

type sampleGroup struct {
	CPU       float64
	Memory    float64
	Container map[string]float64
}

func mustRead(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return b
}

func counter(path, name string) float64 {
	for _, line := range strings.Split(string(mustRead(path)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == name {
			v, _ := strconv.ParseFloat(fields[1], 64)
			return v
		}
	}
	return 0
}

func bytesValue(value string) float64 {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0
	}
	s := fields[0]
	units := []struct {
		Suffix string
		Scale  float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1}}
	for _, unit := range units {
		if strings.HasSuffix(s, unit.Suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(s, unit.Suffix), 64)
			return v * unit.Scale
		}
	}
	return 0
}

func measuredDir(dir string, warmup time.Duration) resources {
	started, _ := time.Parse(time.RFC3339, strings.TrimSpace(string(mustRead(filepath.Join(dir, "started-at.txt")))))
	finished, _ := time.Parse(time.RFC3339, strings.TrimSpace(string(mustRead(filepath.Join(dir, "finished-at.txt")))))
	duration := finished.Sub(started).Seconds()
	before, _ := filepath.Glob(filepath.Join(dir, "*-cgroup-before.txt"))
	byContainer := map[string]float64{}
	cpuSeconds := 0.0
	for _, path := range before {
		name := strings.TrimSuffix(filepath.Base(path), "-cgroup-before.txt")
		after := filepath.Join(dir, name+"-cgroup-after.txt")
		delta := (counter(after, "usage_usec") - counter(path, "usage_usec")) / 1e6
		byContainer[name] = delta
		cpuSeconds += delta
	}

	groups := map[string]*sampleGroup{}
	f, err := os.Open(filepath.Join(dir, "docker-stats.ndjson"))
	if err != nil {
		panic(err)
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row map[string]string
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, row["sampledAt"])
		if err != nil || ts.Before(started.Add(warmup)) {
			continue
		}
		group := groups[row["sampledAt"]]
		if group == nil {
			group = &sampleGroup{Container: map[string]float64{}}
			groups[row["sampledAt"]] = group
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(row["CPUPerc"], "%"), 64)
		group.CPU += cpu
		group.Memory += bytesValue(row["MemUsage"])
		group.Container[row["Name"]] = cpu
	}
	f.Close()
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	avgCPU, peakCPU, peakMem, idleMem := 0.0, 0.0, 0.0, 0.0
	containerTotals := map[string]float64{}
	for i, key := range keys {
		group := groups[key]
		if i == 0 {
			idleMem = group.Memory
		}
		avgCPU += group.CPU
		peakCPU = math.Max(peakCPU, group.CPU)
		peakMem = math.Max(peakMem, group.Memory)
		for name, cpu := range group.Container {
			containerTotals[name] += cpu
		}
	}
	containerAverage := map[string]float64{}
	if len(keys) > 0 {
		avgCPU /= float64(len(keys))
		for name, total := range containerTotals {
			containerAverage[name] = total / float64(len(keys))
		}
	}
	return resources{duration, cpuSeconds, byContainer, cpuSeconds / duration * 100, avgCPU, peakCPU, idleMem, peakMem, len(keys), containerAverage}
}

func loadJSON(path string) map[string]any {
	var value map[string]any
	if err := json.Unmarshal(mustRead(path), &value); err != nil {
		panic(err)
	}
	return value
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: summarize_official RAW_DIR OUTPUT.json")
		os.Exit(2)
	}
	root, output := os.Args[1], os.Args[2]
	out := map[string]any{"behavior": map[string]any{}, "latency": map[string]any{}, "load": []any{}}
	behavior := out["behavior"].(map[string]any)
	latency := out["latency"].(map[string]any)
	loads := out["load"].([]any)
	for _, engine := range []string{"camunda", "bpmngo"} {
		behavior[engine] = measuredDir(filepath.Join(root, engine, "behavior"), 0)
		latency[engine] = []any{
			loadJSON(filepath.Join(root, engine, "latency-pass1", "result.json"))["summary"],
			loadJSON(filepath.Join(root, engine, "latency-pass2", "result.json"))["summary"],
		}
		for _, pass := range []int{1, 2} {
			for _, clients := range []int{10, 50, 100} {
				dir := filepath.Join(root, engine, fmt.Sprintf("load-%d-pass%d", clients, pass))
				result := loadJSON(filepath.Join(dir, "result.json"))
				res := measuredDir(dir, 10*time.Second)
				completed := result["completed"].(float64)
				seconds := result["seconds"].(float64)
				cpuPerInstance := res.CPUAveragePercentSample / 100 * seconds / completed
				loads = append(loads, map[string]any{"engine": engine, "pass": pass, "clients": clients, "result": result, "resources": res, "cpu_seconds_per_instance": cpuPerInstance})
			}
		}
	}
	out["load"] = loads
	b, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(output, append(b, '\n'), 0o644); err != nil {
		panic(err)
	}
}
