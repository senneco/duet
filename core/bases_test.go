package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/senneco/duet/vcs"
)

func baseGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, data, err)
	}
	return string(data)
}
func baseRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	baseGit(t, root, "init", "-q")
	baseGit(t, root, "config", "user.name", "Test")
	baseGit(t, root, "config", "user.email", "test@example.invalid")
	baseGit(t, root, "config", "commit.gpgsign", "false")
	return root
}
func baseWrite(t *testing.T, root, path, text string) string {
	t.Helper()
	p := filepath.Join(root, path)
	must(t, os.WriteFile(p, []byte(text), 0o644))
	return p
}
func baseCommit(t *testing.T, root string) {
	t.Helper()
	baseGit(t, root, "add", ".")
	baseGit(t, root, "commit", "-qm", "fixture")
}
func baseSnapshot(t *testing.T, root string, paths ...string) vcs.Snapshot {
	t.Helper()
	s := vcs.Collect(context.Background(), vcs.Git{}, root, paths)
	if !s.Complete() {
		t.Fatalf("%+v", s)
	}
	return s
}

func TestRepositoryBasePriorityAndHistory(t *testing.T) {
	root := baseRepo(t)
	path := baseWrite(t, root, "a", "committed\n")
	baseCommit(t, root)
	baseWrite(t, root, "a", "review\n")
	c, dir := open(t)
	r := create(t, c, Submission{Path: path, Content: "review\n", Original: orig("wrong\n")})
	_, err := c.AddComment(r, path, &Range{Start: 0, End: 6}, "keep this", nil, false)
	must(t, err)
	f := c.s.Files[path]
	comments, _ := json.Marshal(f.Comments)
	current := f.Current
	history := append([]Version(nil), f.Versions...)
	s := baseSnapshot(t, root)
	must(t, c.SetRepositoryBases(r, s))
	view := fv(t, c, r, path)
	if view.Base != "committed\n" || view.Current != "review\n" || view.BaseSource.Kind != "repository" || view.BaseSource.Revision != s.Revision {
		t.Fatalf("%+v", view)
	}
	after, _ := json.Marshal(f.Comments)
	if !reflect.DeepEqual(comments, after) || current != f.Current || !reflect.DeepEqual(history, f.Versions[:len(history)]) {
		t.Fatal("history or comments changed")
	}
	n := len(f.Versions)
	must(t, c.SetRepositoryBases(r, s))
	if len(f.Versions) != n {
		t.Fatal("duplicate snapshot appended versions")
	}
	// Новый коммит равен текущему тексту, но не принимает файл.
	baseCommit(t, root)
	next := baseSnapshot(t, root, "a")
	must(t, c.SetRepositoryBases(r, next))
	view = fv(t, c, r, path)
	if view.State == StateAccepted || view.Base != "review\n" || view.BaseSource.Revision == s.Revision {
		t.Fatalf("automatic acceptance: %+v", view)
	}
	must(t, c.AcceptFile(r, path))
	if f.BaseSource.Kind != "owner" || f.BaseIsOriginal {
		t.Fatal("explicit acceptance not recorded")
	}
	accepted := f.Base
	records := len(f.BaseHistory)
	baseWrite(t, root, "a", "later commit\n")
	baseCommit(t, root)
	baseWrite(t, root, "a", "later disk\n")
	c = reopen(t, dir)
	must(t, c.SetRepositoryBases(r, baseSnapshot(t, root)))
	f = c.s.Files[path]
	if f.Base != accepted || f.BaseSource.Kind != "owner" || len(f.BaseHistory) != records {
		t.Fatal("accepted base replaced after restart")
	}
	if view := fv(t, c, r, path); view.Base != "review\n" || view.Current != "review\n" {
		t.Fatal(view)
	}
	// Переподключение тоже не влияет на базу.
	must(t, c.Connect(r))
	must(t, c.Disconnect(r))
	if c.s.Files[path].Base != accepted {
		t.Fatal("reconnect changed base")
	}
}

func TestRepositoryAbsentEmptyDeletedAndOutside(t *testing.T) {
	root := baseRepo(t)
	empty := baseWrite(t, root, "empty", "")
	deleted := baseWrite(t, root, "deleted", "old")
	baseCommit(t, root)
	baseWrite(t, root, "empty", "new")
	must(t, os.Remove(deleted))
	newPath := baseWrite(t, root, "new", "")
	outside := filepath.Join(t.TempDir(), "outside")
	c, _ := open(t)
	r := create(t, c, Submission{Path: empty, Content: "new", Original: orig("wrong")}, Submission{Path: deleted, Deleted: true}, Submission{Path: newPath, Content: "", Original: orig("wrong")}, Submission{Path: outside, Content: "draft", Original: orig("external original")})
	s := baseSnapshot(t, root, outside)
	must(t, c.SetRepositoryBases(r, s))
	if v := fv(t, c, r, empty); !v.HasBase || v.Base != "" {
		t.Fatal(v)
	}
	if v := fv(t, c, r, deleted); v.Base != "old" || !v.CurrentDeleted {
		t.Fatal(v)
	}
	if v := fv(t, c, r, newPath); v.HasBase || v.BaseSource.Kind != "repository" {
		t.Fatal(v)
	}
	if err := c.SetOriginal(r, newPath, *orig("wrong again")); !errors.Is(err, ErrHasBase) {
		t.Fatal(err)
	}
	if v := fv(t, c, r, outside); v.Base != "external original" || v.BaseSource.Kind != "agent" {
		t.Fatal(v)
	}
	must(t, c.SetRepositoryBases(r, vcs.Collect(context.Background(), vcs.Git{}, filepath.Dir(outside), nil)))
	if v := fv(t, c, r, outside); v.Base != "external original" {
		t.Fatal(v)
	}
	must(t, c.AcceptFile(r, deleted))
	baseWrite(t, root, "deleted", "recreated")
	baseCommit(t, root)
	must(t, c.SetRepositoryBases(r, baseSnapshot(t, root, "empty", "deleted", "new")))
	if !c.s.Files[deleted].Versions[c.s.Files[deleted].Base].Deleted {
		t.Fatal("accepted deletion overwritten")
	}
}

func TestRepositoryBaseDoesNotChangeOpenResponseOrEditor(t *testing.T) {
	root := baseRepo(t)
	path := baseWrite(t, root, "a", "base")
	baseCommit(t, root)
	baseWrite(t, root, "a", "disk")
	c, _ := open(t)
	r := create(t, c, Submission{Path: path, Content: "draft", Original: orig("agent base")})
	s := baseSnapshot(t, root)
	must(t, c.SetEditing(r, true))
	if err := c.SetRepositoryBases(r, s); !errors.Is(err, ErrEditing) {
		t.Fatal(err)
	}
	must(t, c.SetEditing(r, false))
	must(t, c.AgentStart(r))
	if err := c.SetRepositoryBases(r, s); !errors.Is(err, ErrAgentWorking) {
		t.Fatal(err)
	}
	_, err := c.AgentFinish(r, []Submission{{Path: path, Content: "response"}}, nil)
	must(t, err)
	before, _ := json.Marshal(c.s)
	if err := c.SetRepositoryBases(r, s); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err)
	}
	after, _ := json.Marshal(c.s)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("pending response or source changed")
	}
	must(t, c.AcceptResponse(r))
	must(t, c.SetRepositoryBases(r, s))
	if v := fv(t, c, r, path); v.Current != "response" || v.Base != "base" {
		t.Fatal(v)
	}
}

func TestIncompleteOrForeignSnapshotCannotPartiallyChangeBases(t *testing.T) {
	root := baseRepo(t)
	a := baseWrite(t, root, "a", "base")
	b := baseWrite(t, root, "b", "base")
	baseCommit(t, root)
	baseWrite(t, root, "a", "changed")
	baseWrite(t, root, "b", "changed")
	c, _ := open(t)
	r := create(t, c, Submission{Path: a, Content: "draft", Original: orig("old")})
	other := create(t, c, Submission{Path: b, Content: "draft", Original: orig("old")})
	s := baseSnapshot(t, root)
	before, _ := json.Marshal(c.s)
	if err := c.SetRepositoryBases(r, s); err == nil {
		t.Fatal("foreign file allowed")
	}
	after, _ := json.Marshal(c.s)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("partial mutation")
	}
	if v := fv(t, c, other, b); v.Base != "old" {
		t.Fatal(v)
	}
	s.Entries = s.Entries[:1]
	s.Consistent = false
	if err := c.SetRepositoryBases(r, s); err == nil {
		t.Fatal("inconsistent snapshot allowed")
	}
	s.Consistent = true
	s.Diagnostics = []vcs.Diagnostic{{Code: "original", Message: "read failed"}}
	if err := c.SetRepositoryBases(r, s); err == nil {
		t.Fatal("failed original allowed")
	}
	if v := fv(t, c, r, a); v.Base != "old" {
		t.Fatal(v)
	}
}

func TestExplicitAcceptanceWhenAlreadyEqualIsDurable(t *testing.T) {
	c, dir := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "same", Original: orig("same")})
	must(t, c.AcceptFile(r, "/a"))
	c = reopen(t, dir)
	f := c.s.Files["/a"]
	if f.BaseSource.Kind != "owner" || f.BaseIsOriginal || len(f.BaseHistory) != 2 {
		t.Fatalf("%+v", f)
	}
}

func TestLegacyBaseMigrationPreservesDataAndDiagnosesAmbiguity(t *testing.T) {
	c, dir := open(t)
	r := create(t, c, Submission{Path: "/none", Content: "new"}, Submission{Path: "/owner", Content: "accepted", Original: orig("old")}, Submission{Path: "/original", Content: "draft", Original: orig("old")}, Submission{Path: "/equal", Content: "same", Original: orig("same")})
	must(t, c.AcceptFile(r, "/owner"))
	_, err := c.AddComment(r, "/original", nil, "keep", nil, false)
	must(t, err)
	// Воспроизводим старый AcceptFile: при равенстве исходнику запись не менялась.
	beforeVersions, _ := json.Marshal(c.s.Files["/original"].Versions)
	beforeComments, _ := json.Marshal(c.s.Files["/original"].Comments)
	data, err := os.ReadFile(c.path)
	must(t, err)
	var raw map[string]any
	must(t, json.Unmarshal(data, &raw))
	for _, item := range raw["files"].(map[string]any) {
		f := item.(map[string]any)
		delete(f, "baseSource")
		delete(f, "baseHistory")
	}
	data, err = json.Marshal(raw)
	must(t, err)
	must(t, os.WriteFile(c.path, data, 0o644))
	c = reopen(t, dir)
	for path, kind := range map[string]string{"/none": "none", "/owner": "owner", "/original": "unknown", "/equal": "unknown"} {
		if v := fv(t, c, r, path); v.BaseSource.Kind != kind {
			t.Fatalf("%s: %+v", path, v)
		}
	}
	afterComments, _ := json.Marshal(c.s.Files["/original"].Comments)
	afterVersions, _ := json.Marshal(c.s.Files["/original"].Versions)
	if !reflect.DeepEqual(beforeVersions, afterVersions) || !reflect.DeepEqual(beforeComments, afterComments) {
		t.Fatal("legacy history changed")
	}
	snap := vcs.Snapshot{Protocol: 1, Consistent: true, Workspace: &vcs.Workspace{Provider: "test", Root: "/", ID: "test-root"}, Entries: []vcs.Entry{{Change: vcs.Change{Path: "original"}, Original: vcs.Content{Kind: "text", Text: "repository"}}}}
	var sourceErr *BaseSourceError
	if err := c.SetRepositoryBases(r, snap); !errors.As(err, &sourceErr) {
		t.Fatalf("missing diagnostic: %v", err)
	}
	if sourceErr.Reason == "" || fv(t, c, r, "/original").Base != "old" {
		t.Fatal("ambiguous base guessed")
	}
	must(t, c.AcceptFile(r, "/original"))
	c = reopen(t, dir)
	if fv(t, c, r, "/original").BaseSource.Kind != "owner" {
		t.Fatal("explicit decision not persisted")
	}
}
