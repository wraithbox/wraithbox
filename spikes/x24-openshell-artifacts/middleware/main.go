// Throwaway spike code for X24-openshell-artifacts.
//
// Does Go code generated from OpenShell's supervisor_middleware.proto fit a
// gate that runs inside wb-proxyd? The gate implements the generated
// SupervisorMiddlewareServer interface, and the proxy calls it as a plain Go
// method, with no listener, no gRPC connection and no serialization. The same
// value is then served over an in-memory gRPC connection to show that the
// one implementation also works as an OpenShell external middleware.
package main

import (
	"context"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	mw "example.invalid/x24/gen/middlewarev1"
)

// gate refuses npm tarball downloads of one "too young" version.
type gate struct {
	mw.UnimplementedSupervisorMiddlewareServer
}

func (gate) EvaluateHttpRequest(_ context.Context, req *mw.HttpRequestEvaluation) (*mw.HttpRequestResult, error) {
	t := req.GetTarget()
	if t.GetHost() == "registry.npmjs.org" && strings.HasSuffix(t.GetPath(), "/-/left-pad-9.9.9.tgz") {
		return &mw.HttpRequestResult{
			Decision:   mw.Decision_DECISION_DENY,
			ReasonCode: "dependency_too_young",
			Findings:   []*mw.Finding{{Type: "dep_gate.min_age", Label: "version younger than minimum age", Count: 1}},
		}, nil
	}
	return &mw.HttpRequestResult{Decision: mw.Decision_DECISION_ALLOW}, nil
}

func request(path string) *mw.HttpRequestEvaluation {
	return &mw.HttpRequestEvaluation{
		Phase:  mw.SupervisorMiddlewarePhase_SUPERVISOR_MIDDLEWARE_PHASE_PRE_CREDENTIALS,
		Target: &mw.HttpRequestTarget{Scheme: "https", Host: "registry.npmjs.org", Port: 443, Method: "GET", Path: path},
	}
}

func main() {
	ctx := context.Background()
	var g mw.SupervisorMiddlewareServer = gate{}

	// In process: a method call.
	for _, p := range []string{"/left-pad/-/left-pad-1.3.0.tgz", "/left-pad/-/left-pad-9.9.9.tgz"} {
		res, err := g.EvaluateHttpRequest(ctx, request(p))
		fmt.Printf("in-process %s -> %s %s err=%v\n", p, res.GetDecision(), res.GetReasonCode(), err)
	}

	// Same value as an OpenShell external middleware, over an in-memory gRPC
	// connection.
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	mw.RegisterSupervisorMiddlewareServer(srv, g)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	client := mw.NewSupervisorMiddlewareClient(conn)
	res, err := client.EvaluateHttpRequest(ctx, request("/left-pad/-/left-pad-9.9.9.tgz"))
	fmt.Printf("grpc       /left-pad/-/left-pad-9.9.9.tgz -> %s %s err=%v\n", res.GetDecision(), res.GetReasonCode(), err)

	// Methods the gate does not implement answer Unimplemented.
	_, err = g.Describe(ctx, &mw.MiddlewareDescribeRequest{})
	fmt.Printf("Describe (not implemented) err=%v\n", err)
}
