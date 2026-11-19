package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebDoesNotReclaimKickedSession(t *testing.T) {
	f := setup(t, "claude")
	w := Who{Session: "s2", Agent: "claude", Cwd: f.dir}
	if _, err := f.s.Connect(w, f.r); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Web(WebReq{Who: Who{Session: "s1", Cwd: f.dir}, Paths: []string{f.path}})
	if err == nil || !strings.Contains(err.Error(), "explicit handoff") {
		t.Fatal(err)
	}
	if se, _ := f.c.SessionInfo("s2"); se.Review != f.r {
		t.Fatal(se)
	}
}

// Проверяем пользовательские точки входа: Web, Done и Stop-hook, без submit новых файлов.
func TestRepositoryCollectionThroughService(t *testing.T) {
	f := setup(t, "claude")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", f.dir}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %s %v", b, err)
		}
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	git("add", ".")
	git("commit", "-qm", "fixture")
	write := func(name string) string {
		p := filepath.Join(f.dir, name)
		if err := os.WriteFile(p, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	b := write("web.md")
	w := Who{Session: "s1", Agent: "claude", Cwd: f.dir}
	id, err := f.s.Web(WebReq{Who: w, Paths: []string{f.path}})
	if err != nil || id != f.r {
		t.Fatal(id, err)
	}
	if _, err = f.c.File(id, b); err != nil {
		t.Fatal(err)
	}
	d := write("done.md")
	if _, err = f.s.Done(DoneReq{Who: w}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.c.File(id, d); err != nil {
		t.Fatal(err)
	}
	h := write("hook.md")
	f.hook("Stop", nil)
	if _, err = f.c.File(id, h); err != nil {
		t.Fatal(err)
	}
	v, err := f.c.Review(id)
	if err != nil || v.Completeness == nil || !v.Completeness.Complete || v.Accepted {
		t.Fatal(v, err)
	}
	if err = os.WriteFile(filepath.Join(f.dir, "unsupported"), []byte{0}, 0600); err != nil {
		t.Fatal(err)
	}
	text, err := f.s.Done(DoneReq{Who: w})
	if err != nil || !strings.Contains(text, "completeness is not confirmed") || !strings.Contains(text, "unsupported") {
		t.Fatal(text, err)
	}
}

func TestNewChatContinuesReviewInSameWorkspace(t *testing.T) {
	f := setup(t, "claude")
	f.hook("Stop", nil)
	w := Who{Session: "s2", Agent: "claude", Cwd: f.dir}
	if id, err := f.s.Web(WebReq{Who: w}); err != nil || id != f.r {
		t.Fatalf("web returned %q, %v, want %s", id, err, f.r)
	}
	f.c.Detach(w.Session)
	w.Session = "s3"
	path := filepath.Join(f.dir, "plan.md")
	if err := os.WriteFile(path, []byte("plan"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Submit(SubmitReq{Who: w, Files: []SubmitFile{{Path: path, Content: "plan"}}}); err != nil {
		t.Fatal(err)
	}
	if id := f.s.ReviewOf(w.Session); id != f.r {
		t.Fatalf("submit joined %q, want %s", id, f.r)
	}
}
