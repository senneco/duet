package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/senneco/duet/core"
)

func (e *env) fileView(review, path string) core.FileView {
	e.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/reviews/%s/file?path=%s", e.info().Port, review, url.QueryEscape(path)))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var f core.FileView
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil || resp.StatusCode != http.StatusOK {
		e.t.Fatalf("file: %v, HTTP %d", err, resp.StatusCode)
	}
	return f
}

// Новый чат в том же каталоге находит нужное ревью, публикует правки и только потом открывает его.
func TestWebReplacementSession(t *testing.T) {
	e := setup(t)
	dir := project(t)
	path := filepath.Join(dir, "a.md")
	os.WriteFile(path, []byte("one\n"), 0o644)
	manifest := `{"title":"Первая задача","files":[{"path":"a.md","original":{"text":"zero\n","source":"до задачи"}}]}`
	if r := e.agentRun(dir, "s1", "", "submit", manifest); r.code != 0 {
		t.Fatal(r)
	}
	e.hook(dir, "s1", "Stop", nil)
	cid := e.owner("r1", "comment", map[string]any{"path": path, "text": "Не потерять"})["id"]
	// Второе ревью в той же папке исключает выбор только по каталогу и времени.
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("b\n"), 0o644)
	second := e.agentRun(dir, "other", "", "submit", "b.md")
	if second.code != 0 {
		t.Fatal(second)
	}
	secondID := strings.TrimSuffix(strings.Fields(second.out)[2], ":")
	e.hook(dir, "other", "Stop", nil)
	os.WriteFile(path, []byte("two\n"), 0o644)
	if r := e.agentRun(dir, "s2", "", "web"); r.code != 1 || !strings.Contains(r.err, "--files") || !strings.Contains(r.err, "/r/r1") || !strings.Contains(r.err, "/r/"+secondID) {
		t.Fatal(r)
	}
	if len(e.openedLinks()) != 0 {
		t.Fatal("открылась пустая страница")
	}
	if r := e.agentRun(dir, "s2", "", "reviews", "a.md"); r.code != 0 || !strings.Contains(r.out, "/r/r1") || strings.Contains(r.out, "/r/"+secondID) {
		t.Fatal(r)
	}
	if r := e.agentRun(dir, "s2", "", "web", "--files", "a.md", "b.md"); r.code != 1 {
		t.Fatal("неоднозначность: ", r)
	}
	if len(e.openedLinks()) != 0 {
		t.Fatal("браузер открылся до разрешения неоднозначности")
	}
	if r := e.agentRun(dir, "s2", "", "web", "--files", "a.md"); r.code != 0 || !strings.Contains(r.out, "/r/r1") {
		t.Fatal(r)
	}
	f := e.fileView("r1", path)
	if f.Base != "zero\n" || f.Current != "one\n" || f.Response == nil || *f.Response != "two\n" || len(f.Comments) != 1 || f.Comments[0].ID != cid {
		t.Fatalf("правки и история: %+v", f)
	}
	if r := e.agentRun(dir, "s2", "", "status"); r.code != 0 || !strings.Contains(r.out, "/r/r1") {
		t.Fatal(r)
	}
	if got := e.openedLinks(); len(got) != 1 || got[0] != fmt.Sprintf("http://duet.localhost:%d/r/r1", e.info().Port) {
		t.Fatal(got)
	}
	// Уже подключённый чат тоже передаёт правки до открытия.
	os.WriteFile(path, []byte("three\n"), 0o644)
	if r := e.agentRun(dir, "s2", "", "web"); r.code != 0 {
		t.Fatal(r)
	}
	if f := e.fileView("r1", path); f.Response == nil || *f.Response != "three\n" || f.Base != "zero\n" {
		t.Fatal(f)
	}
}

func TestWebDoesNotOpenOnBlockedPublication(t *testing.T) {
	e := setup(t)
	dir := project(t)
	path := filepath.Join(dir, "a.md")
	os.WriteFile(path, []byte("one\n"), 0o644)
	e.agentRun(dir, "s1", "", "submit", "a.md")
	if r := e.agentRun(dir, "s2", "", "web", "--files", "a.md"); r.code != 1 || !strings.Contains(r.err, "another agent") {
		t.Fatal(r)
	}
	e.hook(dir, "s1", "Stop", nil)
	if r := e.agentRun(dir, "s2", "", "web", "--files", "a.md", "missing.md"); r.code != 1 || !strings.Contains(r.err, "submit") {
		t.Fatal(r)
	}
	if r := e.agentRun(dir, "s2", "", "web", "--files", "new-task.md"); r.code != 1 || !strings.Contains(r.err, "duet submit") {
		t.Fatal(r)
	}
	if len(e.openedLinks()) != 0 {
		t.Fatal(e.openedLinks())
	}
	if r := e.agentRun(dir, "s2", "", "status"); r.code != 1 {
		t.Fatal("отказ не должен подключать: ", r)
	}
}

func TestWebDoesNotOpenOnReadError(t *testing.T) {
	e := setup(t)
	dir := project(t)
	path := filepath.Join(dir, "a.md")
	os.WriteFile(path, []byte("one\n"), 0o644)
	e.agentRun(dir, "s1", "", "submit", "a.md")
	e.hook(dir, "s1", "Stop", nil)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := e.agentRun(dir, "s2", "", "web", "--files", "a.md"); r.code != 1 {
		t.Fatal("ошибка чтения не должна выглядеть успехом: ", r)
	}
	if len(e.openedLinks()) != 0 {
		t.Fatal(e.openedLinks())
	}
}
