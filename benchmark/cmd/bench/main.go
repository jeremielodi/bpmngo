// Command bench drives both Camunda 8 and BpmnGo through the same public
// Zeebe gRPC wire protocol. It emits machine-readable JSON; orchestration and
// cgroup sampling live in benchmark/scripts.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/jeremielodi/bpmngo/internal/zeebegrpc/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type config struct {
	mode, engine, address, rest, user, password, output string
	iterations, clients, workers, batch                 int
	warmup, duration                                    time.Duration
}

type runner struct {
	cfg    config
	conn   *grpc.ClientConn
	client pb.GatewayClient
	seq    atomic.Int64
}

type sample struct {
	DurationUS int64  `json:"duration_us"`
	Code       string `json:"code"`
	Error      string `json:"error,omitempty"`
}

type latencyResult struct {
	Engine      string              `json:"engine"`
	GeneratedAt string              `json:"generated_at"`
	Iterations  int                 `json:"iterations"`
	Calls       map[string][]sample `json:"calls"`
	Summary     map[string]summary  `json:"summary"`
}

type summary struct {
	Count  int     `json:"count"`
	Errors int     `json:"errors"`
	P50US  int64   `json:"p50_us"`
	P95US  int64   `json:"p95_us"`
	P99US  int64   `json:"p99_us"`
	MeanUS float64 `json:"mean_us"`
}

type loadResult struct {
	Engine        string         `json:"engine"`
	GeneratedAt   string         `json:"generated_at"`
	Clients       int            `json:"clients"`
	Workers       int            `json:"workers"`
	Batch         int            `json:"batch"`
	WarmupSeconds float64        `json:"warmup_seconds"`
	Seconds       float64        `json:"seconds"`
	Completed     int64          `json:"completed"`
	Throughput    float64        `json:"instances_per_second"`
	Errors        map[string]int `json:"errors"`
	Latency       summary        `json:"latency"`
}

func main() {
	cfg := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, cfg.address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		fatal("connect %s: %v", cfg.address, err)
	}
	defer conn.Close()
	r := &runner{cfg: cfg, conn: conn, client: pb.NewGatewayClient(conn)}

	var out any
	switch cfg.mode {
	case "probe":
		out = r.probe()
	case "latency":
		out = r.latency()
	case "load":
		out = r.load()
	case "behavior":
		out = r.behavior()
	default:
		fatal("unknown -mode %q", cfg.mode)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fatal("marshal: %v", err)
	}
	if cfg.output == "" || cfg.output == "-" {
		fmt.Println(string(b))
		return
	}
	if err := os.WriteFile(cfg.output, append(b, '\n'), 0o644); err != nil {
		fatal("write %s: %v", cfg.output, err)
	}
}

func parseFlags() config {
	var c config
	flag.StringVar(&c.mode, "mode", "probe", "probe, latency, or load")
	flag.StringVar(&c.engine, "engine", "unknown", "result label")
	flag.StringVar(&c.address, "address", "127.0.0.1:26500", "Zeebe gRPC address")
	flag.StringVar(&c.rest, "rest", "http://127.0.0.1:8080", "Camunda 8 REST base URL")
	flag.StringVar(&c.user, "user", "", "HTTP Basic username sent as gRPC metadata")
	flag.StringVar(&c.password, "password", "", "HTTP Basic password sent as gRPC metadata")
	flag.StringVar(&c.output, "output", "-", "JSON output file")
	flag.IntVar(&c.iterations, "iterations", 30, "sequential latency samples")
	flag.IntVar(&c.clients, "clients", 10, "closed-loop load clients")
	flag.IntVar(&c.workers, "workers", 8, "job workers")
	flag.IntVar(&c.batch, "batch", 32, "maximum jobs per activation")
	flag.DurationVar(&c.warmup, "warmup", 10*time.Second, "load warmup")
	flag.DurationVar(&c.duration, "duration", 60*time.Second, "measured load duration")
	flag.Parse()
	return c
}

func (r *runner) ctx(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if r.cfg.user != "" {
		v := base64.StdEncoding.EncodeToString([]byte(r.cfg.user + ":" + r.cfg.password))
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Basic "+v))
	}
	return ctx, cancel
}

func (r *runner) probe() any {
	ctx, cancel := r.ctx(10 * time.Second)
	defer cancel()
	t, err := r.client.Topology(ctx, &pb.TopologyRequest{})
	if err != nil {
		return map[string]any{"engine": r.cfg.engine, "code": grpcCode(err), "error": err.Error()}
	}
	return map[string]any{
		"engine": r.cfg.engine, "gatewayVersion": t.GetGatewayVersion(), "clusterSize": t.GetClusterSize(),
		"partitions": t.GetPartitionsCount(), "replicationFactor": t.GetReplicationFactor(), "brokers": len(t.GetBrokers()),
	}
}

func (r *runner) latency() latencyResult {
	res := latencyResult{Engine: r.cfg.engine, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Iterations: r.cfg.iterations, Calls: map[string][]sample{}}

	for i := 0; i < r.cfg.iterations; i++ {
		res.Calls["Topology"] = append(res.Calls["Topology"], timed(func() error {
			ctx, cancel := r.ctx(10 * time.Second)
			defer cancel()
			_, err := r.client.Topology(ctx, &pb.TopologyRequest{})
			return err
		}))
	}

	for i := 0; i < r.cfg.iterations; i++ {
		id := fmt.Sprintf("LatencyDeploy_%s_%d", safeID(r.cfg.engine), r.seq.Add(1))
		model := endProcess(id)
		res.Calls["DeployResource"] = append(res.Calls["DeployResource"], timed(func() error {
			_, err := r.deploy(id+".bpmn", model)
			return err
		}))
	}

	plainID := "LatencyCreate_" + safeID(r.cfg.engine)
	if _, err := r.deploy(plainID+".bpmn", endProcess(plainID)); err != nil {
		res.Calls["setup"] = append(res.Calls["setup"], errorSample(err))
	} else {
		for i := 0; i < r.cfg.iterations; i++ {
			res.Calls["CreateProcessInstance"] = append(res.Calls["CreateProcessInstance"], timed(func() error {
				ctx, cancel := r.ctx(10 * time.Second)
				defer cancel()
				_, err := r.client.CreateProcessInstance(ctx, &pb.CreateProcessInstanceRequest{BpmnProcessId: plainID, Version: -1, Variables: `{}`})
				return err
			}))
		}
		for i := 0; i < r.cfg.iterations; i++ {
			res.Calls["CreateProcessInstanceWithResult"] = append(res.Calls["CreateProcessInstanceWithResult"], timed(func() error {
				ctx, cancel := r.ctx(30 * time.Second)
				defer cancel()
				_, err := r.client.CreateProcessInstanceWithResult(ctx, &pb.CreateProcessInstanceWithResultRequest{Request: &pb.CreateProcessInstanceRequest{BpmnProcessId: plainID, Version: -1, Variables: `{}`}, RequestTimeout: 25000})
				return err
			}))
		}
	}

	jobID := "LatencyJob_" + safeID(r.cfg.engine)
	if _, err := r.deploy(jobID+".bpmn", serviceProcess(jobID, "latency-job", 1)); err != nil {
		res.Calls["setup"] = append(res.Calls["setup"], errorSample(err))
	} else {
		for i := 0; i < r.cfg.iterations; i++ {
			ctx, cancel := r.ctx(10 * time.Second)
			start := time.Now()
			_, createErr := r.client.CreateProcessInstance(ctx, &pb.CreateProcessInstanceRequest{BpmnProcessId: jobID, Version: -1, Variables: `{}`})
			cancel()
			if createErr != nil {
				res.Calls["JobDelivery"] = append(res.Calls["JobDelivery"], errorSample(createErr))
				continue
			}
			job, err := r.activateOne("latency-job", 15*time.Second, 1)
			res.Calls["JobDelivery"] = append(res.Calls["JobDelivery"], sampleFrom(start, err))
			if err != nil {
				continue
			}
			res.Calls["CompleteJob"] = append(res.Calls["CompleteJob"], timed(func() error {
				ctx, cancel := r.ctx(10 * time.Second)
				defer cancel()
				_, err := r.client.CompleteJob(ctx, &pb.CompleteJobRequest{JobKey: job.GetKey(), Variables: `{}`})
				return err
			}))
		}
	}
	res.Summary = map[string]summary{}
	for name, samples := range res.Calls {
		res.Summary[name] = summarize(samples)
	}
	return res
}

func (r *runner) load() loadResult {
	id := "LoadTwoTasks_" + safeID(r.cfg.engine)
	if _, err := r.deploy(id+".bpmn", serviceProcess(id, "load-job", 2)); err != nil {
		fatal("load deployment: %v", err)
	}
	stopWorkers := make(chan struct{})
	var workerWG sync.WaitGroup
	for i := 0; i < r.cfg.workers; i++ {
		workerWG.Add(1)
		go func() { defer workerWG.Done(); r.worker("load-job", stopWorkers) }()
	}
	runPhase := func(d time.Duration, collect bool) ([]sample, map[string]int) {
		deadline := time.Now().Add(d)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var samples []sample
		errs := map[string]int{}
		for i := 0; i < r.cfg.clients; i++ {
			wg.Add(1)
			go func(client int) {
				defer wg.Done()
				for time.Now().Before(deadline) {
					s := timed(func() error {
						ctx, cancel := r.ctx(60 * time.Second)
						defer cancel()
						_, err := r.client.CreateProcessInstanceWithResult(ctx, &pb.CreateProcessInstanceWithResultRequest{Request: &pb.CreateProcessInstanceRequest{BpmnProcessId: id, Version: -1, Variables: fmt.Sprintf(`{"client":%d,"amount":42,"region":"EU"}`, client)}, RequestTimeout: 55000})
						return err
					})
					if collect {
						mu.Lock()
						samples = append(samples, s)
						if s.Code != codes.OK.String() {
							errs[s.Code]++
						}
						mu.Unlock()
					}
				}
			}(i)
		}
		wg.Wait()
		return samples, errs
	}
	_, _ = runPhase(r.cfg.warmup, false)
	started := time.Now()
	samples, errs := runPhase(r.cfg.duration, true)
	elapsed := time.Since(started)
	close(stopWorkers)
	workerWG.Wait()
	var completed int64
	for _, s := range samples {
		if s.Code == codes.OK.String() {
			completed++
		}
	}
	return loadResult{Engine: r.cfg.engine, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Clients: r.cfg.clients, Workers: r.cfg.workers, Batch: r.cfg.batch, WarmupSeconds: r.cfg.warmup.Seconds(), Seconds: elapsed.Seconds(), Completed: completed, Throughput: float64(completed) / elapsed.Seconds(), Errors: errs, Latency: summarize(samples)}
}

func (r *runner) worker(jobType string, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		ctx, cancel := r.ctx(5 * time.Second)
		stream, err := r.client.ActivateJobs(ctx, &pb.ActivateJobsRequest{Type: jobType, Worker: "fair-bench", Timeout: 30000, MaxJobsToActivate: int32(r.cfg.batch), RequestTimeout: 3000})
		if err != nil {
			cancel()
			time.Sleep(10 * time.Millisecond)
			continue
		}
		resp, err := stream.Recv()
		cancel()
		if err != nil && !errors.Is(err, io.EOF) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if resp == nil {
			continue
		}
		for _, job := range resp.GetJobs() {
			ctx, cancel := r.ctx(10 * time.Second)
			_, _ = r.client.CompleteJob(ctx, &pb.CompleteJobRequest{JobKey: job.GetKey(), Variables: `{"workerResult":true}`})
			cancel()
		}
	}
}

func (r *runner) deploy(name, model string) (*pb.DeployResourceResponse, error) {
	ctx, cancel := r.ctx(30 * time.Second)
	defer cancel()
	return r.client.DeployResource(ctx, &pb.DeployResourceRequest{Resources: []*pb.Resource{{Name: name, Content: []byte(model)}}})
}

func (r *runner) activateOne(jobType string, timeout time.Duration, max int32) (*pb.ActivatedJob, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := r.ctx(3 * time.Second)
		stream, err := r.client.ActivateJobs(ctx, &pb.ActivateJobsRequest{Type: jobType, Worker: "latency-worker", Timeout: 10000, MaxJobsToActivate: max, RequestTimeout: 2000})
		if err != nil {
			cancel()
			continue
		}
		resp, err := stream.Recv()
		cancel()
		if err == nil && len(resp.GetJobs()) > 0 {
			return resp.GetJobs()[0], nil
		}
	}
	return nil, status.Error(codes.DeadlineExceeded, "no job received")
}

func timed(fn func() error) sample { start := time.Now(); return sampleFrom(start, fn()) }
func sampleFrom(start time.Time, err error) sample {
	s := sample{DurationUS: time.Since(start).Microseconds(), Code: grpcCode(err)}
	if err != nil {
		s.Error = err.Error()
	}
	return s
}
func errorSample(err error) sample { return sample{Code: grpcCode(err), Error: err.Error()} }
func grpcCode(err error) string {
	if err == nil {
		return codes.OK.String()
	}
	return status.Code(err).String()
}

func summarize(samples []sample) summary {
	var us []int64
	var total int64
	errors := 0
	for _, s := range samples {
		if s.Code == codes.OK.String() {
			us = append(us, s.DurationUS)
			total += s.DurationUS
		} else {
			errors++
		}
	}
	sort.Slice(us, func(i, j int) bool { return us[i] < us[j] })
	q := func(p float64) int64 {
		if len(us) == 0 {
			return 0
		}
		return us[int(math.Ceil(p*float64(len(us))))-1]
	}
	mean := 0.0
	if len(us) > 0 {
		mean = float64(total) / float64(len(us))
	}
	return summary{Count: len(samples), Errors: errors, P50US: q(.50), P95US: q(.95), P99US: q(.99), MeanUS: mean}
}

func safeID(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
	return s
}
func fatal(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...); os.Exit(1) }

func definitions(id, body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Definitions_` + id + `" targetNamespace="http://benchmark.local/` + id + `">
<bpmn:process id="` + id + `" isExecutable="true">` + body + `</bpmn:process></bpmn:definitions>`
}
func endProcess(id string) string {
	return definitions(id, `<bpmn:startEvent id="start"/><bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="end"/><bpmn:endEvent id="end"/>`)
}
func serviceProcess(id, jobType string, tasks int) string {
	var b strings.Builder
	b.WriteString(`<bpmn:startEvent id="start"/>`)
	prev := "start"
	for i := 1; i <= tasks; i++ {
		task := fmt.Sprintf("task%d", i)
		b.WriteString(fmt.Sprintf(`<bpmn:sequenceFlow id="f%d" sourceRef="%s" targetRef="%s"/><bpmn:serviceTask id="%s"><bpmn:extensionElements><zeebe:taskDefinition type="%s" retries="3"/></bpmn:extensionElements></bpmn:serviceTask>`, i, prev, task, task, jobType))
		prev = task
	}
	b.WriteString(fmt.Sprintf(`<bpmn:sequenceFlow id="f%d" sourceRef="%s" targetRef="end"/><bpmn:endEvent id="end"/>`, tasks+1, prev))
	return definitions(id, b.String())
}
