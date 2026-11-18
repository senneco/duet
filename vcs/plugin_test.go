package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func scriptPlugin(t *testing.T, body string) Executable {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin with spaces")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return Executable{Name: "fixture", Path: path}
}
func replyPlugin(t *testing.T, reply string) Executable {
	return scriptPlugin(t, "cat >/dev/null\ncat <<'JSON'\n"+reply+"\nJSON")
}
func TestPluginTransportFailures(t *testing.T) {
	for name, reply := range map[string]string{
		"malformed": "broken", "version": `{"protocol":2,"result":null}`,
		"missing": "{\"protocol\":1}", "trailing": `{"protocol":1,"result":null} {}`,
		"error":         `{"protocol":1,"error":"cannot read repository"}`,
		"contradiction": `{"protocol":1,"error":"bad","result":null}`,
		"workspace":     `{"protocol":1,"result":{"root":"/","provider":"fixture","id":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := Collect(context.Background(), replyPlugin(t, reply), t.TempDir(), nil)
			if s.Complete() || len(s.Diagnostics) == 0 {
				t.Fatalf("failure hidden: %+v", s)
			}
		})
	}
	for name, body := range map[string]string{"exit": "exit 4", "timeout": "exec sleep 10", "oversize": "exec yes x"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := scriptPlugin(t, body).Detect(ctx, t.TempDir())
			if err == nil || time.Since(start) > 2*time.Second {
				t.Fatalf("unbounded call: %v", err)
			}
		})
	}
	p := Executable{Name: "missing", Path: filepath.Join(t.TempDir(), "absent")}
	if _, err := p.Detect(context.Background(), t.TempDir()); err == nil {
		t.Fatal("missing process accepted")
	}
}
func TestPluginResultValidation(t *testing.T) {
	for _, result := range []string{`null`, `{}`, `{"revision":"","token":"x"}`, `{"revision":"","token":"x","changes":null}`, `{"revision":"","token":"x","changes":[{"path":"../escape","status":"M"}]}`, `{"revision":"","token":"x","changes":[],"diagnostics":[{"path":"../escape","code":"bad","message":"bad"}]}`} {
		p := replyPlugin(t, `{"protocol":1,"result":`+result+`}`)
		if _, err := p.State(context.Background(), Workspace{}); err == nil {
			t.Fatalf("accepted state %s", result)
		}
	}
	for _, result := range []string{`null`, `{}`, `{"kind":"absent","text":"lost"}`, `{"kind":"unsupported"}`, `{"kind":"text","mode":"120000"}`} {
		p := replyPlugin(t, `{"protocol":1,"result":`+result+`}`)
		if _, err := p.Original(context.Background(), Workspace{}, "revision", "file"); err == nil {
			t.Fatalf("accepted content %s", result)
		}
	}
}
func TestExternalGitEquivalenceAndRegistration(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "example plugin")
	cmd := exec.Command("go", "build", "-o", bin, "./example")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	root := repo(t)
	for _, path := range []string{"empty", "a", "gone", "old"} {
		put(t, root, path, "")
	}
	commit(t, root)
	put(t, root, "a", "staged")
	run(t, root, "add", "a")
	put(t, root, "a", "disk")
	run(t, root, "mv", "old", "renamed")
	os.Remove(filepath.Join(root, "gone"))
	put(t, root, "new\n☀", "new")
	r := Registry{Home: t.TempDir()}
	ctx := context.Background()
	// Наличие программы в рабочей копии ничего не регистрирует.
	put(t, root, "duet-plugin", "not executable")
	w, err := r.Detect(ctx, root)
	if err != nil || w.Provider != "git" {
		t.Fatal(w, err)
	}
	p := Executable{Name: "example-git", Path: bin}
	if err := r.Add(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(ctx, p); err == nil {
		t.Fatal("registration replaced")
	}
	builtin := Collect(ctx, Git{}, root, nil)
	external := Collect(ctx, r, root, nil)
	if !external.Complete() {
		t.Fatalf("%+v", external)
	}
	external.Workspace.Provider = "git"
	if !reflect.DeepEqual(builtin, external) {
		t.Fatalf("snapshots differ:\n%+v\n%+v", builtin, external)
	}
	// Повреждённая регистрация не скрывается за встроенным git.
	os.WriteFile(filepath.Join(r.dir(), "broken.json"), []byte("{"), 0600)
	if s := Collect(ctx, r, root, nil); s.Complete() {
		t.Fatal("bad registry hidden")
	}
	os.Remove(filepath.Join(r.dir(), "broken.json"))
	os.Remove(bin)
	if s := Collect(ctx, r, root, nil); s.Complete() {
		t.Fatal("missing executable hidden")
	}
	if err := r.Remove(p.Name); err != nil {
		t.Fatal(err)
	}
	if w, err := r.Detect(ctx, root); err != nil || w.Provider != "git" {
		t.Fatal(w, err)
	}
}
func TestRegistryAmbiguityAndNoRepository(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r := Registry{Home: t.TempDir()}
	for _, name := range []string{"one", "two"} {
		p := scriptPlugin(t, `input=$(cat)
case "$input" in
 *hello*) echo '{"protocol":1,"result":{"operations":["detect","state","original"]}}';;
 *) echo '{"protocol":1,"result":{"provider":"`+name+`","root":"`+root+`","id":"id"}}';;
esac`)
		p.Name = name
		if err := r.Add(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Detect(context.Background(), root); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatal(err)
	}
	r.Remove("one")
	r.Remove("two")
	p := replyPlugin(t, `{"protocol":1,"result":null}`)
	if w, err := p.Detect(context.Background(), root); err != nil || w != nil {
		t.Fatal(w, err)
	}
}
