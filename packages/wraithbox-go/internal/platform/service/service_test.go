package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The test binary runs itself as the command Exec runs, with
// helperEnv set (the os/exec helper process pattern).
const helperEnv = "WB_SERVICE_TEST_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "env":
		// Print the environment the command got, one variable a line.
		fmt.Println(strings.Join(os.Environ(), "\n"))
		os.Exit(3)
	case "flood":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("x", 4*maxOutput)))
		os.Exit(0)
	}
}

func TestExecGivesOnlyTheCommandsEnvironment(t *testing.T) {
	t.Setenv("WB_SERVICE_TEST_LEAK", "leaked")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Exec(context.Background(), Command{Path: exe, Env: []string{helperEnv + "=env", "PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 3 {
		t.Errorf("Code = %d, want 3", res.Code)
	}
	out := string(res.Output)
	if strings.Contains(out, "WB_SERVICE_TEST_LEAK") || !strings.Contains(out, "PATH=/usr/bin:/bin") {
		t.Errorf("the command's environment is\n%s\nwant only its own", out)
	}
}

func TestExecCapsOutput(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Exec(context.Background(), Command{Path: exe, Env: []string{helperEnv + "=flood"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 || len(res.Output) != maxOutput {
		t.Errorf("Code %d, %d bytes of output; want 0 and %d", res.Code, len(res.Output), maxOutput)
	}
}

func TestExecRefusesARelativePath(t *testing.T) {
	if _, err := Exec(context.Background(), Command{Path: "launchctl"}); err == nil {
		t.Fatal("Exec ran a relative path")
	}
}
