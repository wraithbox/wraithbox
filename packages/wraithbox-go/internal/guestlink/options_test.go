package guestlink

import (
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

// TestLimitsValues: the values of S04-08-records-and-bounds, "RPCs from
// `wb-guestd`", and no limit left at zero, which grpc-go would read as
// its own default.
func TestLimitsValues(t *testing.T) {
	want := limits{
		maxMsgSize:        64 << 10,
		maxHeaderListSize: 4 << 10,
		streamWindow:      64 << 10,
		connWindow:        64 << 10,
		bufferSize:        16 << 10,
		callTimeout:       10 * time.Second,
		maxStreams:        16,
		keepaliveMinTime:  time.Minute,
		keepaliveTime:     time.Minute,
		keepaliveTimeout:  20 * time.Second,
		handshakeTimeout:  5 * time.Second,
	}
	if guestLimits != want {
		t.Errorf("guestLimits = %+v, want %+v", guestLimits, want)
	}
	v := reflect.ValueOf(guestLimits)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Errorf("limit %s is zero, so grpc-go would use its default", v.Type().Field(i).Name)
		}
	}
}

// TestOptionsSetEveryLimit: the client and the server options of a
// guest connection each set every limit explicitly, so none is left at
// grpc-go's default (S04-08-records-and-bounds, "RPCs from
// `wb-guestd`").
func TestOptionsSetEveryLimit(t *testing.T) {
	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "client",
			got:  optionNames(clientOptions(guestLimits, nil)),
			want: []string{
				"dialer", "credentials", "authority", "user-agent", "retry", "idle",
				"message size", "header list size", "stream window", "connection window",
				"read buffer", "write buffer", "call deadline",
			},
		},
		{
			name: "server",
			got:  optionNames(serverOptions(guestLimits, newRPCLimiter("work", DefaultRPCLimits(), discard, time.Now))),
			want: []string{
				"credentials", "receive size", "send size", "header list size",
				"stream window", "connection window", "read buffer", "write buffer",
				"streams", "keepalive enforcement", "keepalive", "handshake",
				"unary bounds", "stream bounds",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.Sorted(slices.Values(tt.got))
			want := slices.Sorted(slices.Values(tt.want))
			if !slices.Equal(got, want) {
				t.Errorf("options %q, want %q", got, want)
			}
			if len(slices.Compact(got)) != len(tt.got) {
				t.Errorf("an option is set twice: %q", tt.got)
			}
		})
	}
}

func optionNames[T any](opts []option[T]) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		if reflect.ValueOf(&o.opt).Elem().IsNil() {
			continue
		}
		out = append(out, o.name)
	}
	return out
}
