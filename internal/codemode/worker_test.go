package codemode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Build the real miodesk executable so the reserved worker CLI path is
// exercised; Go test binaries do not dispatch hidden CLI commands.
func workerBinary(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("resource-limited worker currently Linux only")
	}
	bin := filepath.Join(t.TempDir(), "miodesk")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "../../cmd/miodesk")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, out)
	}
	return bin
}

func TestIsolatedWorkerComposesAndBrokersReadOnlyCalls(t *testing.T) {
	bin := workerBinary(t)
	ws := fixture(t)
	source := `async () => {
  const result=await tools.search({query:"NEEDLE",path:"src"});
  const first=result.matches[0];
  return {line:first.line,text:(await tools.read({path:first.file,offset:first.line,limit:1})).content.trim()};
 }`
	out, err := runIsolatedBinary(context.Background(), ws, source, bin)
	if err != nil {
		t.Fatal(err)
	}
	if out.Calls != 2 || !strings.Contains(string(out.Value), "NEEDLE") {
		t.Fatalf("wrong result: %+v", out)
	}
}

func TestIsolatedWorkerRejectsUnauthorizedToolsAndPathEscape(t *testing.T) {
	bin := workerBinary(t)
	ws := fixture(t)
	for _, source := range []string{
		`async () => await tools.write({path:"src/hacked",content:"bad"})`,
		`async () => await tools.read({path:"../../../etc/passwd"})`,
		`async () => require("fs").readFileSync("/etc/passwd")`,
		`async () => await tools.exec_command({cmd:"id"})`,
	} {
		if _, err := runIsolatedBinary(context.Background(), ws, source, bin); err == nil {
			t.Errorf("unsafe request succeeded: %s", source)
		}
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), "src", "hacked")); !os.IsNotExist(err) {
		t.Fatalf("script wrote file: %v", err)
	}
}

func TestIsolatedWorkerTimeoutAndCleanup(t *testing.T) {
	bin := workerBinary(t)
	ws := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runIsolatedBinary(ctx, ws, `async () => {for(;;){} }`, bin)
	if err == nil {
		t.Fatal("infinite loop was accepted")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("worker did not terminate promptly: %v", time.Since(start))
	}
}

// The heap limit is Linux process resource accounting rather than a full
// filesystem sandbox. It should reject oversized JS allocations without
// crashing the host process running these tests.
func TestIsolatedWorkerMemoryGuard(t *testing.T) {
	bin := workerBinary(t)
	ws := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runIsolatedBinary(ctx, ws, `async () => {const x=new Array(50000000).fill("lots"); return x.length;}`, bin)
	if err == nil {
		t.Fatal("oversized JS allocation exceeded worker memory budget but succeeded")
	}
}
