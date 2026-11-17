package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/senneco/duet/core"
)

// bin — собранная команда duet: фоновый запуск проверяем на настоящей программе.
var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "duet-bin")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "duet")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/duet").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t      *testing.T
	home   string
	port   int
	opened string // куда пишет «браузер»: по ссылке на строку
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func setup(t *testing.T) *env {
	t.Helper()
	// Короткий путь: в macOS временные папки длинные, а нам важна только изоляция.
	home, err := os.MkdirTemp("", "duet-home")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, home: home, port: freePort(t), opened: filepath.Join(home, "opened.txt")}
	t.Cleanup(func() {
		e.run(bin, "stop")
		os.RemoveAll(home)
	})
	return e
}

// browser — подставной браузер: записывает ссылку в opened.txt, а не открывает вкладку у владельца.
func (e *env) browser() string {
	b := filepath.Join(e.home, "browser.sh")
	os.WriteFile(b, []byte(fmt.Sprintf("#!/bin/sh\necho \"$1\" >> %q\n", e.opened)), 0o755)
	return b
}

// run запускает команду и ждёт её. Если ядро держит вывод команды, run зависнет — значит, терминал занят.
func (e *env) run(prog string, args ...string) (string, error) {
	e.t.Helper()
	cmd := exec.Command(prog, args...)
	cmd.Env = append(os.Environ(), "DUET_HOME="+e.home, fmt.Sprintf("DUET_PORT=%d", e.port), "DUET_BROWSER="+e.browser(),
		"CODEX_THREAD_ID=", "CLAUDE_CODE_SESSION_ID=", "DUET_SESSION=")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return out.String(), fmt.Errorf("%v: %s", err, errb.String())
		}
		return out.String(), nil
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		e.t.Fatalf("duet %v не вернул терминал", args)
		return "", nil
	}
}

func (e *env) web(args ...string) string {
	e.t.Helper()
	out, err := e.run(bin, append([]string{"web"}, args...)...)
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func (e *env) info() info {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.home, "server.json"))
	if err != nil {
		e.t.Fatalf("server.json: %v", err)
	}
	var in info
	json.Unmarshal(data, &in)
	return in
}

func (e *env) openedLinks() []string {
	data, _ := os.ReadFile(e.opened)
	return strings.Fields(string(data))
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func (e *env) health(port int) (string, bool) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var h struct{ Build string }
	json.NewDecoder(resp.Body).Decode(&h)
	return h.Build, true
}

// review кладёт ревью в папку данных, пока ядро не запущено.
func (e *env) review(title string) string {
	e.t.Helper()
	c, err := core.Open(e.home)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _, err := c.CreateReview(title, []core.Submission{{Path: "wiki:" + title, Content: "текст\n"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// Сценарии 92–94: команда сама запускает ядро в фоне и возвращает терминал; ядро одно;
// `duet web` всегда открывает новую вкладку.
func TestWebStartsOneCore(t *testing.T) {
	e := setup(t)
	base := fmt.Sprintf("http://duet.localhost:%d/", e.port)

	if link := e.web(); link != base {
		t.Fatalf("ссылка: %q", link)
	}
	first := e.info()
	if !alive(first.PID) {
		t.Fatal("ядро не работает после команды")
	}
	if _, ok := e.health(e.port); !ok {
		t.Fatal("ядро не отвечает")
	}

	e.web()
	if second := e.info(); second.PID != first.PID {
		t.Fatalf("запустилось второе ядро: %d и %d", first.PID, second.PID)
	}
	if got := e.openedLinks(); len(got) != 2 || got[0] != base || got[1] != base {
		t.Fatalf("вкладки: %v", got)
	}
}

// Сценарии 94, 95, 98: ревью по ссылке; ссылка та же после перезапуска; `duet stop` ничего не теряет.
func TestReviewLinkSurvivesRestart(t *testing.T) {
	e := setup(t)
	id := e.review("задача")
	want := fmt.Sprintf("http://duet.localhost:%d/r/%s", e.port, id)
	if link := e.web(id); link != want {
		t.Fatalf("ссылка: %q, ждали %q", link, want)
	}
	pid := e.info().PID

	out, err := e.run(bin, "stop")
	if err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: %q %v", out, err)
	}
	if alive(pid) {
		t.Fatal("ядро не остановилось")
	}
	if _, ok := e.health(e.port); ok {
		t.Fatal("ядро всё ещё отвечает")
	}
	if out, _ := e.run(bin, "stop"); !strings.Contains(out, "not running") {
		t.Fatalf("повторный stop: %q", out)
	}

	if link := e.web(id); link != want {
		t.Fatalf("после перезапуска: %q", link)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/reviews/%s", e.port, id))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("ревью после перезапуска: %v %v", resp, err)
	}
	resp.Body.Close()

	if _, err := e.run(bin, "web", "r999"); err == nil || !strings.Contains(err.Error(), "no review") {
		t.Fatalf("несуществующее ревью: %v", err)
	}
}

// Сценарий 99: поставили новую версию — работающее старое ядро само заменяется новым.
func TestNewVersionReplacesCore(t *testing.T) {
	e := setup(t)
	e.web()
	old := e.info()

	// «Новая версия» — та же программа в другом месте: отпечаток сборки другой.
	newer := filepath.Join(e.home, "duet-new")
	data, _ := os.ReadFile(bin)
	os.WriteFile(newer, data, 0o755)
	if _, err := e.run(newer, "web"); err != nil {
		t.Fatal(err)
	}
	cur := e.info()
	if cur.PID == old.PID || cur.Build == old.Build {
		t.Fatalf("ядро не заменилось: было %+v, стало %+v", old, cur)
	}
	if alive(old.PID) {
		time.Sleep(200 * time.Millisecond)
		if alive(old.PID) {
			t.Fatal("старое ядро ещё работает")
		}
	}
	if b, _ := e.health(e.port); b != cur.Build {
		t.Fatalf("отвечает не новое ядро: %s", b)
	}
	e.run(newer, "stop")
}

// Сценарий 100: порт занят другой программой — duet работает на другом и пишет ссылку с ним.
func TestBusyPort(t *testing.T) {
	e := setup(t)
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", e.port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	link := e.web()
	port := e.info().Port
	if port == e.port || link != fmt.Sprintf("http://duet.localhost:%d/", port) {
		t.Fatalf("порт %d, ссылка %q", port, link)
	}
	if _, ok := e.health(port); !ok {
		t.Fatal("ядро не отвечает на новом порту")
	}
}
