package vcs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, data, err)
	}
	return strings.TrimSpace(string(data))
}
func put(t *testing.T, root, path, text string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
func repo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, root, "init", "-q")
	run(t, root, "config", "user.name", "Test")
	run(t, root, "config", "user.email", "test@example.invalid")
	run(t, root, "config", "commit.gpgsign", "false")
	run(t, root, "config", "core.autocrlf", "false")
	return root
}
func commit(t *testing.T, root string) string {
	t.Helper()
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", "fixture")
	return run(t, root, "rev-parse", "HEAD")
}
func collect(t *testing.T, root string, extra ...string) Snapshot {
	t.Helper()
	s := Collect(context.Background(), Git{}, root, extra)
	if !s.Complete() {
		t.Fatalf("incomplete: %+v", s)
	}
	return s
}
func entries(s Snapshot) map[string]Entry {
	m := map[string]Entry{}
	for _, e := range s.Entries {
		m[e.Path] = e
	}
	return m
}

func TestWholeRootIndexAndDisk(t *testing.T) {
	root := repo(t)
	for _, p := range []string{"one/a", "two/b", "staged", "back", "empty", "deleted", "old name"} {
		put(t, root, p, "original\n")
	}
	put(t, root, "empty", "")
	put(t, root, ".gitignore", "ignored\n")
	rev := commit(t, root)
	put(t, root, "one/a", "edited\n")
	put(t, root, "two/b", "neighbor\n")
	put(t, root, "staged", "index\n")
	put(t, root, "back", "index\n")
	run(t, root, "add", "staged", "back")
	put(t, root, "staged", "disk\n")
	put(t, root, "back", "original\n")
	run(t, root, "mv", "old name", "new name")
	if err := os.Remove(filepath.Join(root, "deleted")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "empty", "not empty")
	put(t, root, "new\nfile\t☀", "")
	put(t, root, "ignored", "secret")
	indexPath := filepath.Join(root, ".git", "index")
	before, _ := os.ReadFile(indexPath)
	stamp, _ := os.Stat(indexPath)
	s := collect(t, filepath.Join(root, "one"), "a")
	after, _ := os.ReadFile(indexPath)
	afterStamp, _ := os.Stat(indexPath)
	if !reflect.DeepEqual(before, after) || !stamp.ModTime().Equal(afterStamp.ModTime()) {
		t.Fatal("collection wrote index")
	}
	if s.Revision != rev || s.Workspace.Root != root {
		t.Fatalf("%+v", s)
	}
	m := entries(s)
	if len(m) != 9 {
		t.Fatalf("paths: %+v", m)
	}
	if _, ok := m["ignored"]; ok {
		t.Fatal("ignored included")
	}
	for path, want := range map[string]string{"one/a": "edited\n", "two/b": "neighbor\n", "staged": "disk\n", "back": "original\n"} {
		e := m[path]
		if e.Current.Text != want || e.Original.Text != "original\n" {
			t.Fatalf("%s: %+v", path, e)
		}
	}
	if m["back"].Status != "MM" {
		t.Fatalf("lost staged difference: %+v", m["back"])
	}
	if m["empty"].Original.Kind != "text" || m["empty"].Original.Text != "" {
		t.Fatal("empty original lost")
	}
	if m["deleted"].Current.Kind != "absent" || m["deleted"].Original.Text != "original\n" {
		t.Fatal("deletion lost")
	}
	if m["new name"].PreviousPath != "old name" || m["old name"].Original.Text != "original\n" || m["old name"].Current.Kind != "absent" {
		t.Fatal("rename source lost")
	}
	if m["new name"].Original.Kind != "absent" || m["new\nfile\t☀"].Current.Kind != "text" {
		t.Fatal("new file lost")
	}
	extra := entries(collect(t, root, "ignored", ".gitignore"))
	if extra["ignored"].Current.Text != "secret" || extra[".gitignore"].Original.Kind != "text" {
		t.Fatal("explicit materials lost")
	}
	if run(t, root, "rev-parse", "HEAD") != rev {
		t.Fatal("HEAD changed")
	}
}

func TestWorktreesAndEnvironmentIsolation(t *testing.T) {
	root := repo(t)
	put(t, root, "a", "base")
	rev := commit(t, root)
	other := filepath.Join(t.TempDir(), "other")
	run(t, root, "worktree", "add", "--detach", other, rev)
	put(t, root, "a", "main")
	put(t, other, "a", "other")
	put(t, other, "only-other", "x")
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	t.Setenv("GIT_WORK_TREE", root)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(root, ".git", "index"))
	a, b := collect(t, root), collect(t, other)
	if a.Workspace.ID == b.Workspace.ID || len(a.Entries) != 1 || len(b.Entries) != 2 {
		t.Fatalf("mixed worktrees: %+v %+v", a, b)
	}
	if entries(a)["a"].Current.Text != "main" || entries(b)["a"].Current.Text != "other" {
		t.Fatal("wrong worktree contents")
	}
}

func TestNoRepositoryUnbornAndBrokenRepository(t *testing.T) {
	dir := t.TempDir()
	s := collect(t, dir, "note")
	if s.Workspace != nil || len(s.External) != 1 {
		t.Fatal(s)
	}
	root := repo(t)
	put(t, root, "new", "")
	s = collect(t, root)
	if s.Revision != "" || s.Entries[0].Original.Kind != "absent" {
		t.Fatal(s)
	}
	broken := t.TempDir()
	put(t, broken, ".git", "gitdir: missing\n")
	s = Collect(context.Background(), Git{}, broken, nil)
	if s.Complete() || len(s.Diagnostics) == 0 || s.Diagnostics[0].Code != "detect" {
		t.Fatal(s)
	}
	t.Setenv("PATH", t.TempDir())
	s = Collect(context.Background(), Git{}, root, nil)
	if s.Complete() || s.Diagnostics[0].Code != "detect" {
		t.Fatal(s)
	}
}

func TestUnsupportedAndHiddenChanges(t *testing.T) {
	root := repo(t)
	put(t, root, "binary", "\x00data")
	put(t, root, "hidden", "base")
	if err := os.Symlink("binary", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	commit(t, root)
	put(t, root, "binary", "\x00changed")
	put(t, root, "hidden", "changed")
	run(t, root, "update-index", "--assume-unchanged", "hidden")
	os.Remove(filepath.Join(root, "link"))
	os.Symlink("hidden", filepath.Join(root, "link"))
	nested := filepath.Join(root, "nested")
	os.Mkdir(nested, 0o755)
	run(t, nested, "init", "-q")
	put(t, nested, "inside", "x")
	s := Collect(context.Background(), Git{}, root, nil)
	if s.Complete() || !s.Consistent {
		t.Fatal(s)
	}
	paths := map[string]bool{}
	for _, d := range s.Diagnostics {
		paths[d.Path] = true
	}
	for _, p := range []string{"binary", "hidden", "link", "nested"} {
		if !paths[p] {
			t.Fatalf("no reason for %s: %+v", p, s)
		}
	}
	// Материал внутри самостоятельного репозитория не читается как файл внешнего.
	s = Collect(context.Background(), Git{}, root, []string{"nested/inside"})
	if entries(s)["nested/inside"].Current.Kind != "unsupported" {
		t.Fatal(s)
	}
}

func TestOriginalPinnedAndReadErrorNotAbsence(t *testing.T) {
	root := repo(t)
	put(t, root, "a", "old")
	rev := commit(t, root)
	put(t, root, "a", "new")
	commit(t, root)
	w, _ := (Git{}).Detect(context.Background(), root)
	c, err := (Git{}).Original(context.Background(), *w, rev, "a")
	if err != nil || c.Text != "old" {
		t.Fatal(c, err)
	}
	_, err = (Git{}).Original(context.Background(), *w, "deadbeef", "a")
	if err == nil {
		t.Fatal("missing commit treated as missing file")
	}
	put(t, root, ".git/HEAD", "broken\n")
	if s := Collect(context.Background(), Git{}, root, nil); s.Complete() {
		t.Fatal("broken HEAD treated as unborn")
	}
}

type changingProvider struct {
	Provider
	onState           func(int)
	onOriginal        func(int)
	states, originals int
	stateErrorAt      int
	originalError     bool
}

func (p *changingProvider) State(ctx context.Context, w Workspace) (State, error) {
	p.states++
	if p.onState != nil {
		p.onState(p.states)
	}
	if p.states == p.stateErrorAt {
		return State{}, fmt.Errorf("state unavailable")
	}
	return p.Provider.State(ctx, w)
}
func (p *changingProvider) Original(ctx context.Context, w Workspace, rev, path string) (Content, error) {
	p.originals++
	if p.onOriginal != nil {
		p.onOriginal(p.originals)
	}
	if p.originalError {
		return Content{}, fmt.Errorf("original unavailable")
	}
	return p.Provider.Original(ctx, w, rev, path)
}
func TestCollectionRetriesChangingContentsListIndexAndHead(t *testing.T) {
	for _, scenario := range []string{"contents", "list", "index", "head", "always"} {
		t.Run(scenario, func(t *testing.T) {
			root := repo(t)
			put(t, root, "a", "base")
			commit(t, root)
			put(t, root, "a", "draft")
			p := &changingProvider{Provider: Git{}}
			p.onState = func(n int) {
				if n != 2 && scenario != "always" {
					return
				}
				switch scenario {
				case "contents", "always":
					put(t, root, "a", fmt.Sprintf("changed %d", n))
				case "list":
					put(t, root, "new", "new")
				case "index":
					run(t, root, "add", "a")
				case "head":
					commit(t, root)
				}
			}
			s := Collect(context.Background(), p, root, []string{"a"})
			if scenario == "always" {
				if s.Complete() || s.Consistent || s.Attempts != MaxAttempts || s.Diagnostics[len(s.Diagnostics)-1].Code != "changed" {
					t.Fatal(s)
				}
			} else if !s.Complete() || s.Attempts != 2 {
				t.Fatalf("%+v", s)
			}
			if scenario == "head" && entries(s)["a"].Original.Text != "draft" {
				t.Fatal("stale original")
			}
		})
	}
}
func TestReadAndListFailuresAreExplicit(t *testing.T) {
	root := repo(t)
	put(t, root, "a", "base")
	commit(t, root)
	put(t, root, "a", "draft")
	for _, p := range []*changingProvider{{Provider: Git{}, originalError: true}, {Provider: Git{}, stateErrorAt: 2}} {
		s := Collect(context.Background(), p, root, nil)
		if s.Complete() || len(s.Entries) != 1 || len(s.Diagnostics) == 0 {
			t.Fatal(s)
		}
	}
}

func TestSubmoduleAndExecutableModeAreNotSilentlyReviewedAsText(t *testing.T) {
	root := repo(t)
	put(t, root, "script", "echo hello\n")
	rev := commit(t, root)
	baseIndex := fmt.Sprintf("160000,%s,submodule", rev)
	run(t, root, "update-index", "--add", "--cacheinfo", baseIndex)
	run(t, root, "commit", "-qm", "submodule fixture")
	if err := os.Chmod(filepath.Join(root, "script"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, root, "config", "core.filemode", "true")
	s := Collect(context.Background(), Git{}, root, nil)
	paths := map[string]bool{}
	for _, d := range s.Diagnostics {
		paths[d.Path] = true
	}
	if s.Complete() || !paths["script"] || !paths["submodule"] {
		t.Fatalf("%+v", s)
	}
}

type contractProvider struct {
	workspace *Workspace
	state     State
	original  Content
	detectErr error
}

func (p contractProvider) Detect(context.Context, string) (*Workspace, error) {
	return p.workspace, p.detectErr
}
func (p contractProvider) State(context.Context, Workspace) (State, error) { return p.state, nil }
func (p contractProvider) Original(context.Context, Workspace, string, string) (Content, error) {
	return p.original, nil
}
func TestCommonContractAndBadProviderResults(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "a", "disk")
	good := contractProvider{workspace: &Workspace{Provider: "example", Root: root, ID: "isolated-copy"}, state: State{Revision: "revision-1", Token: "stable", Changes: []Change{{Path: "a", Status: "modified"}}}, original: Content{Kind: "text", Text: "base"}}
	s := Collect(context.Background(), good, root, nil)
	if !s.Complete() || s.Entries[0].Original.Text != "base" || s.Entries[0].Current.Text != "disk" {
		t.Fatal(s)
	}
	for _, kind := range []string{"detect-error", "empty-token", "invalid-path", "invalid-original", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			p := good
			switch kind {
			case "detect-error":
				p.detectErr = fmt.Errorf("provider unavailable")
			case "empty-token":
				p.state.Token = ""
			case "invalid-path":
				p.state.Changes = []Change{{Path: "../outside", Status: "modified"}}
			case "invalid-original":
				p.original = Content{}
			case "duplicate":
				p.state.Changes = []Change{good.state.Changes[0], good.state.Changes[0]}
			}
			s := Collect(context.Background(), p, root, nil)
			if s.Complete() || len(s.Diagnostics) == 0 {
				t.Fatal(s)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s := Collect(ctx, good, root, nil); s.Complete() || s.Diagnostics[0].Code != "cancelled" {
		t.Fatal(s)
	}
}

func TestSymlinkedStartAndExplicitOutsideMaterial(t *testing.T) {
	root := repo(t)
	put(t, root, "a", "base")
	commit(t, root)
	put(t, root, "a", "draft")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	a, b := collect(t, root), collect(t, alias, outside)
	if a.Workspace.ID != b.Workspace.ID || len(b.External) != 1 || b.External[0] != outside {
		t.Fatal(b)
	}
}
