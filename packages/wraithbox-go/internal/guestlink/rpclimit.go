package guestlink

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/ratelog"
)

// The per-VM bounds on requests from wb-guestd, the defaults of
// RPCLimits (S04-08-records-and-bounds, "Host work the guest can cause",
// row "RPCs from `wb-guestd`": values in code).
const (
	// DefaultRPCRate and DefaultRPCBurst: 50 requests a second per VM,
	// with a burst of 100.
	DefaultRPCRate  = 50
	DefaultRPCBurst = 100
	// DefaultRPCInFlight: 32 requests in flight per VM, over every
	// connection of the VM. A stream is in flight until it ends.
	DefaultRPCInFlight = 32
)

// The rules of RPCLimiter's refusals.
const (
	// RuleRPCRate: a request past the VM's request rate.
	RuleRPCRate = "guest-rpc-rate"
	// RuleRPCInFlight: a request while the VM has its bound of requests
	// in flight.
	RuleRPCInFlight = "guest-rpc-inflight"
)

// errorDomain is the ErrorInfo domain of a refusal.
const errorDomain = "wraithbox.hostd"

// RPCLimits are the per-VM bounds of an RPCLimiter.
type RPCLimits struct {
	// Rate is the requests a second, Burst the most at once after a
	// quiet spell.
	Rate  float64
	Burst int
	// InFlight is the most requests running at once.
	InFlight int
}

// DefaultRPCLimits are the bounds wb-hostd runs with.
func DefaultRPCLimits() RPCLimits {
	return RPCLimits{Rate: DefaultRPCRate, Burst: DefaultRPCBurst, InFlight: DefaultRPCInFlight}
}

// RPCLimiter bounds the requests one VM's wb-guestd makes of wb-hostd:
// a token bucket for the rate, and a count of the requests in flight.
// A request over either is refused at once with RESOURCE_EXHAUSTED and
// the rule as the ErrorInfo reason, never queued, and logged with its
// rule at most once per rule per ratelog.Window. Its methods are safe
// for concurrent use.
type RPCLimiter struct {
	vm     string
	limits RPCLimits
	now    func() time.Time
	log    *slog.Logger

	mu       sync.Mutex
	tokens   float64
	last     time.Time
	inFlight int
}

// NewRPCLimiter returns the limiter of the VM named vm.
func NewRPCLimiter(vm string, limits RPCLimits, log *slog.Logger) *RPCLimiter {
	return newRPCLimiter(vm, limits, log, time.Now)
}

func newRPCLimiter(vm string, limits RPCLimits, log *slog.Logger, now func() time.Time) *RPCLimiter {
	return &RPCLimiter{
		vm: vm, limits: limits, now: now, log: ratelog.NewAt(log, ratelog.Window, now),
		tokens: float64(limits.Burst), last: now(),
	}
}

// admit takes a token and an in-flight slot for one request. It returns
// the rule of a refusal, or "" and the release of the slot.
func (l *RPCLimiter) admit() (string, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if elapsed := now.Sub(l.last); elapsed > 0 {
		l.tokens = min(float64(l.limits.Burst), l.tokens+elapsed.Seconds()*l.limits.Rate)
		l.last = now
	}
	if l.tokens < 1 {
		return RuleRPCRate, nil
	}
	// A request refused for the in-flight bound still takes its token,
	// so the refusals too are bounded by the rate.
	l.tokens--
	if l.inFlight >= l.limits.InFlight {
		return RuleRPCInFlight, nil
	}
	l.inFlight++
	var once sync.Once
	return "", func() {
		once.Do(func() {
			l.mu.Lock()
			l.inFlight--
			l.mu.Unlock()
		})
	}
}

// refuse logs a refusal and returns its gRPC error.
func (l *RPCLimiter) refuse(ctx context.Context, rule, method string) error {
	l.log.LogAttrs(ctx, slog.LevelWarn, "guest request refused",
		slog.String("rule", rule), slog.String("decision", "refuse"),
		slog.String("vm", l.vm), slog.String("method", method))
	st, err := status.New(codes.ResourceExhausted, rule).WithDetails(&errdetails.ErrorInfo{Reason: rule, Domain: errorDomain})
	if err != nil {
		return status.Error(codes.ResourceExhausted, rule)
	}
	return st.Err()
}

func (l *RPCLimiter) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	rule, release := l.admit()
	if rule != "" {
		return nil, l.refuse(ctx, rule, info.FullMethod)
	}
	defer release()
	return handler(ctx, req)
}

func (l *RPCLimiter) stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	rule, release := l.admit()
	if rule != "" {
		return l.refuse(ss.Context(), rule, info.FullMethod)
	}
	defer release()
	return handler(srv, ss)
}
