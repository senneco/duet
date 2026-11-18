package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/senneco/duet/vcs"
)

func TestExecutablePluginUsesRepositoryRules(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "example")
	cmd := exec.Command("go", "build", "-o", bin, "../vcs/example")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	root := baseRepo(t)
	p := baseWrite(t, root, "a", "committed")
	baseCommit(t, root)
	baseWrite(t, root, "a", "review")
	home := t.TempDir()
	c, err := Open(home)
	must(t, err)
	// Регистрация после запуска ядра применяется при следующем сборе.
	must(t, (vcs.Registry{Home: home}).Add(context.Background(), vcs.Executable{Name: "example-git", Path: bin}))
	id := create(t, c, Submission{Path: p, Content: "review", Original: orig("wrong")})
	_, err = c.PrepareWeb(Session{ID: "s", Cwd: root}, id, []string{p})
	must(t, err)
	if f := fv(t, c, id, p); f.Base != "committed" {
		t.Fatal(f)
	}
	must(t, c.AcceptFile(id, p))
	baseWrite(t, root, "a", "changed commit")
	baseCommit(t, root)
	fresh := baseWrite(t, root, "fresh", "new")
	_, err = c.Done("s", nil)
	must(t, err)
	if f := fv(t, c, id, p); f.Base != "review" {
		t.Fatal("owner base replaced", f)
	}
	if f := fv(t, c, id, fresh); f.HasBase || f.Current != "new" {
		t.Fatal(f)
	}
	must(t, os.Remove(bin))
	_, err = c.Done("s", nil)
	must(t, err)
	noAccepted(t, c, id)
	if !hasProblem(rv(t, c, id), "detect", "") {
		t.Fatal(rv(t, c, id))
	}
	// Ошибка сохраняется после перезапуска.
	reopened, err := Open(home)
	must(t, err)
	if !hasProblem(rv(t, reopened, id), "detect", "") {
		t.Fatal("diagnostic lost")
	}
}
