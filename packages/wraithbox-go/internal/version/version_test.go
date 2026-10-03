package version

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		want   string
	}{
		{name: "cli", binary: "wb", want: "wb (Wraith Box) 0.0.0-dev"},
		{name: "gateway", binary: "wb-netd", want: "wb-netd (Wraith Box) 0.0.0-dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := String(tt.binary); got != tt.want {
				t.Errorf("String(%q) = %q, want %q", tt.binary, got, tt.want)
			}
		})
	}
}
