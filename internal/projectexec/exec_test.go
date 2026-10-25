package projectexec

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxCommandBoundaries(t *testing.T) {
	if Available() != nil {
		t.Skip("macOS execution sandbox required")
	}
	base := t.TempDir()
	root := filepath.Join(base, "project")
	scratch := filepath.Join(base, "scratch")
	outside := filepath.Join(base, "outside")
	os.MkdirAll(filepath.Join(root, ".git"), 0700)
	os.MkdirAll(outside, 0700)
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	t.Setenv("LLMTOOLS_CONTROL_TOKEN", "must-not-leak")
	command := `printf ok > result; printf cached > "$TMPDIR/check"; test -z "$OPENAI_API_KEY$LLMTOOLS_CONTROL_TOKEN" && printf clean`
	r, err := Run(context.Background(), root, scratch, command, 10)
	if err != nil || r.ExitCode != 0 || r.Stdout != "clean" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, command := range []string{"cat escape/secret", "printf bad > escape/new", "printf bad > .git/config", "curl --max-time 2 -s https://example.com"} {
		r, err := Run(context.Background(), root, scratch, command, 5)
		if err != nil || r.ExitCode == 0 {
			t.Fatalf("boundary %q: %+v %v", command, r, err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	r, err = Run(context.Background(), root, scratch, fmt.Sprintf("curl --noproxy '*' --max-time 2 -s http://%s", listener.Addr().String()), 5)
	if err != nil || r.ExitCode == 0 {
		t.Fatalf("localhost service reachable: %+v %v", r, err)
	}
	r, err = Run(context.Background(), root, scratch, "printf failure; exit 7", 5)
	if err != nil || r.ExitCode != 7 || !strings.Contains(r.Stdout, "failure") {
		t.Fatalf("exit: %+v %v", r, err)
	}
	r, err = Run(context.Background(), root, scratch, "yes output | head -c 300000", 5)
	if err != nil || !r.Truncated || len(r.Stdout) != MaxOutputBytes {
		t.Fatalf("output bound: %+v %v", r, err)
	}
	r, err = Run(context.Background(), root, scratch, "sleep 10", 1)
	if err != nil || !r.TimedOut {
		t.Fatalf("command deadline: %+v %v", r, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r, err = Run(ctx, root, scratch, "sleep 10; printf leaked > late", 20)
	if err == nil {
		t.Fatalf("cancellation: %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "late")); err == nil {
		t.Fatal("cancelled command wrote output")
	}
}

func TestSandboxRunsRealGoTests(t *testing.T) {
	if Available() != nil {
		t.Skip("macOS execution sandbox required")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go not installed")
	}
	root := t.TempDir()
	scratch := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.25\n"), 0600)
	os.WriteFile(filepath.Join(root, "sum_test.go"), []byte("package fixture\nimport (\"testing\";\"net\")\nfunc TestSum(t *testing.T){if 1+1!=2 {t.Fatal(\"sum\")}; l,e:=net.Listen(\"tcp\",\"127.0.0.1:0\");if e!=nil {t.Fatal(e)};l.Close()}\n"), 0600)
	r, err := Run(context.Background(), root, scratch, "go test ./...", 120)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("real test execution: %+v %v", r, err)
	}
}
