package zeebegrpc

import (
	"context"

	"github.com/jeremielodi/bpmngo/internal/metrics"
	"github.com/jeremielodi/bpmngo/internal/runtime"
	"github.com/jeremielodi/bpmngo/internal/zeebegrpc/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newWorkMethods are the Gateway calls that create new work: the only ones
// refused under back-pressure (see runtime.AdmitNewWork). Job completions,
// failures and activations go through, as they finish accepted work.
var newWorkMethods = map[string]bool{
	pb.Gateway_CreateProcessInstance_FullMethodName:           true,
	pb.Gateway_CreateProcessInstanceWithResult_FullMethodName: true,
	pb.Gateway_PublishMessage_FullMethodName:                  true,
	pb.Gateway_BroadcastSignal_FullMethodName:                 true,
}

// BackpressureInterceptor answers RESOURCE_EXHAUSTED, as Zeebe does, to a
// call creating new work while the engine is overloaded; the official
// clients then wait and retry.
func BackpressureInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if newWorkMethods[info.FullMethod] {
			if err := runtime.AdmitNewWork(); err != nil {
				metrics.BackpressureRejections.WithLabelValues("grpc").Inc()
				return nil, status.Error(codes.ResourceExhausted, err.Error())
			}
		}
		return handler(ctx, req)
	}
}
