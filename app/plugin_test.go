package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DUET_HOME", home)
	bin := filepath.Join(t.TempDir(), "example")
	cmd := exec.Command("go", "build", "-o", bin, "../vcs/example")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	root := t.TempDir()
	cmd = exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	invoke := func(want int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"plugin"}, args...), &out, &stderr); code != want {
			t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, &out, &stderr)
		}
		return out.String() + stderr.String()
	}
	invoke(1, "add", "git", bin)
	invoke(0, "add", "example-git", bin)
	if out := invoke(0, "list"); !strings.Contains(out, bin) {
		t.Fatal(out)
	}
	invoke(1, "add", "example-git", bin)
	invoke(0, "check", "example-git", root)
	invoke(1, "check", "example-git", t.TempDir())
	os.Remove(bin)
	invoke(1, "check", "example-git", root)
	invoke(0, "remove", "example-git")
	invoke(1, "check", "example-git", root)
	if _, err := os.Stat(filepath.Join(home, "server.json")); !os.IsNotExist(err) {
		t.Fatal("plugin commands started server")
	}
}
