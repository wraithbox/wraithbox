package platform

import (
	"runtime"
	"testing"
)

func TestGuestSupport(t *testing.T) {
	tests := []struct {
		host, guest OS
		want        Support
	}{
		{MacOS, MacOS, V1},
		{MacOS, Linux, Later},
		{MacOS, Windows, Later},
		{Windows, MacOS, Unsupported},
		{Windows, Linux, Later},
		{Windows, Windows, Later},
		{Linux, MacOS, Unsupported},
		{Linux, Linux, Later},
		{Linux, Windows, Later},
	}
	for _, tt := range tests {
		t.Run(string(tt.host)+"/"+string(tt.guest), func(t *testing.T) {
			if got := GuestSupport(tt.host, tt.guest); got != tt.want {
				t.Errorf("GuestSupport(%s, %s) = %d, want %d", tt.host, tt.guest, got, tt.want)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	wslEnv := func(k string) string {
		if k == "WSL_DISTRO_NAME" {
			return "Ubuntu-24.04"
		}
		return ""
	}
	noEnv := func(string) string { return "" }
	interop := func(p string) bool { return p == "/proc/sys/fs/binfmt_misc/WSLInterop" }
	none := func(string) bool { return false }

	tests := []struct {
		name   string
		goos   string
		getenv func(string) string
		exists func(string) bool
		want   Host
	}{
		{name: "mac", goos: "darwin", getenv: wslEnv, exists: interop, want: Host{OS: MacOS}},
		{name: "windows", goos: "windows", getenv: noEnv, exists: none, want: Host{OS: Windows}},
		{name: "linux", goos: "linux", getenv: noEnv, exists: none, want: Host{OS: Linux}},
		{name: "wsl", goos: "linux", getenv: wslEnv, exists: interop, want: Host{OS: Linux, WSL: true}},
		{name: "wsl variable only", goos: "linux", getenv: wslEnv, exists: none, want: Host{OS: Linux}},
		{name: "interop only", goos: "linux", getenv: noEnv, exists: interop, want: Host{OS: Linux}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detect(tt.goos, tt.getenv, tt.exists); got != tt.want {
				t.Errorf("detect = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestDetectCurrent runs natively on every CI operating system.
func TestDetectCurrent(t *testing.T) {
	want := map[string]OS{"darwin": MacOS, "windows": Windows, "linux": Linux}[runtime.GOOS]
	if got := Detect().OS; got != want {
		t.Errorf("Detect().OS = %q on %s, want %q", got, runtime.GOOS, want)
	}
}
