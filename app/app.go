// Package app — команда duet: ядро в фоне, `duet web`, `duet stop`.
package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/senneco/duet/agent"
)

// DefaultPort — постоянный адрес duet: http://duet.localhost:4848.
const DefaultPort = 4848

// Config — откуда брать данные и как открывать браузер. Переменные окружения нужны для тестов
// и для тех, кто хочет держать данные в другой папке.
type Config struct {
	Home    string // папка данных, DUET_HOME, по умолчанию ~/.duet
	Port    int    // DUET_PORT, по умолчанию 4848
	Browser string // программа, которая открывает ссылку (DUET_BROWSER); по умолчанию системная
}

func FromEnv() Config {
	cfg := Config{Home: os.Getenv("DUET_HOME"), Port: DefaultPort, Browser: os.Getenv("DUET_BROWSER")}
	if cfg.Home == "" {
		home, _ := os.UserHomeDir()
		cfg.Home = filepath.Join(home, ".duet")
	}
	if p, err := strconv.Atoi(os.Getenv("DUET_PORT")); err == nil && p > 0 {
		cfg.Port = p
	}
	if cfg.Browser == "" {
		cfg.Browser = "xdg-open"
		if runtime.GOOS == "darwin" {
			cfg.Browser = "open"
		}
	}
	return cfg
}

// info — что работающее ядро пишет о себе в server.json.
type info struct {
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Build string `json:"build"`
}

func (cfg Config) infoPath() string { return filepath.Join(cfg.Home, "server.json") }
func (cfg Config) lockPath() string { return filepath.Join(cfg.Home, "server.lock") }
func (cfg Config) logPath() string  { return filepath.Join(cfg.Home, "server.log") }

// Build — отпечаток этой сборки duet: путь, размер и время файла программы.
// Поставили новую версию — отпечаток другой, и работающее старое ядро заменяется.
func Build() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	st, err := os.Stat(exe)
	if err != nil {
		return "unknown"
	}
	h := sha256.Sum256(fmt.Appendf(nil, "%s:%d:%d", exe, st.Size(), st.ModTime().UnixNano()))
	return hex.EncodeToString(h[:6])
}

var client = &http.Client{Timeout: 2 * time.Second}

// running — работающее ядро: адрес и отпечаток сборки, которые оно сообщило само.
func (cfg Config) running() (info, bool) {
	data, err := os.ReadFile(cfg.infoPath())
	if err != nil {
		return info{}, false
	}
	var in info
	if json.Unmarshal(data, &in) != nil || in.Port == 0 {
		return info{}, false
	}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", in.Port))
	if err != nil {
		return info{}, false
	}
	defer resp.Body.Close()
	var h struct{ Build string }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil {
		return info{}, false
	}
	in.Build = h.Build
	return in, true
}

// URL — адрес работающего ядра для владельца.
func URL(port int) string { return fmt.Sprintf("http://duet.localhost:%d", port) }

// Ensure возвращает порт работающего ядра или запускает его в фоне.
// Управляемая установка меняет сборку только через явное обновление.
func (cfg Config) Ensure() (int, error) {
	unlock, err := fileLock(filepath.Join(cfg.Home, "installation.lock"), syscall.LOCK_SH)
	if err != nil {
		return 0, err
	}
	defer unlock()
	build := Build()
	if in, ok := cfg.running(); ok {
		if in.Build == build {
			return in.Port, nil
		}
		// Управляемая установка меняет сервер только через явное обновление.
		if _, err := os.Stat(filepath.Join(cfg.Home, "update-source.json")); err == nil {
			return in.Port, nil
		}
		if err := cfg.stop(in); err != nil {
			return 0, err
		}
	}
	if err := cfg.spawn(); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if in, ok := cfg.running(); ok && in.Build == build {
			return in.Port, nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return 0, fmt.Errorf("duet did not start, see %s", cfg.logPath())
}

// spawn запускает `duet serve` отдельно от терминала: команда сразу возвращается,
// а ядро продолжает работать.
func (cfg Config) spawn() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Home, 0o755); err != nil {
		return err
	}
	log, err := os.OpenFile(cfg.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Env = append(os.Environ(), "DUET_HOME="+cfg.Home, "DUET_PORT="+strconv.Itoa(cfg.Port))
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// stop просит ядро остановиться и ждёт, пока оно освободит папку данных.
func (cfg Config) stop(in info) error {
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/shutdown", in.Port), bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !locked(cfg.lockPath()) {
			return nil
		}
		time.Sleep(30 * time.Millisecond)
	}
	return errors.New("duet did not stop")
}

// locked — папку данных держит работающее ядро.
func locked(path string) bool {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return true
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

const usage = `duet — async review of agent work.

  duet web [review]   send pending file changes and open the review; outside an agent session, open the list
  duet web --files F...  find the task's review by its files, connect, send changes and open it
  duet plugin         register, list, remove or check a local VCS plugin
  duet stop           stop the background duet
  duet install        install duet into ~/.local/bin and its skill and hooks into Claude Code and Codex
  duet update         check and apply an update; --check only checks

Agent commands (see the duet skill):
  duet submit         send files for review (JSON manifest as one argument, or file arguments)
  duet connect <link> pick up a review
  duet done           finish a turn: answers to the comments (JSON as one argument)
  duet original <file> --source S --command C | --file F | --text T
  duet take           take new comments and messages now
  duet status         which review this session is connected to
  duet reviews [F...]  find reviews by task files, or list candidates in the current directory
`

// Main — точка входа команды duet. Возвращает код выхода.
func Main(args []string, stdout, stderr io.Writer) int {
	cfg := FromEnv()
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	finishUpdate := cmd == "install" && len(args) == 1 && args[0] == "--finish-update"
	if cmd != "serve" && cmd != "stop" && cmd != "--update-protocol" && !finishUpdate {
		if err = cfg.recoverUpdate(); err != nil {
			fmt.Fprintln(stderr, "duet:", err)
			return 1
		}
	}
	switch cmd {
	case "web":
		err = cfg.web(args, stdout)
	case "stop":
		var unlock func()
		unlock, err = fileLock(filepath.Join(cfg.Home, "installation.lock"), syscall.LOCK_SH)
		if err == nil {
			defer unlock()
			err = cfg.stopCmd(stdout)
		}
	case "serve":
		err = cfg.Serve()
	case "plugin":
		var unlock func()
		unlock, err = fileLock(filepath.Join(cfg.Home, "installation.lock"), syscall.LOCK_SH)
		if err == nil {
			defer unlock()
			err = cfg.plugin(args, stdout)
		}
	case "install":
		switch {
		case len(args) == 2 && args[0] == "--from-release":
			err = cfg.installPublicRelease(args[1])
		case len(args) == 2 && args[0] == "--source-config":
			err = cfg.installSource(args[1])
		case len(args) == 1 && args[0] == "--finish-update":
			err = cfg.install(stdout)
		case len(args) == 0:
			var unlock func()
			unlock, err = fileLock(filepath.Join(cfg.Home, "installation.lock"), syscall.LOCK_EX)
			if err != nil {
				break
			}
			defer unlock()
			if _, running := cfg.running(); running {
				err = errors.New("stop duet before a manual installation, or use duet update")
			} else {
				err = cfg.install(stdout)
			}
		default:
			err = errors.New("usage: duet install")
		}
	case "update":
		err = cfg.update(args, stdout)
	case "--update-protocol":
		fmt.Fprintln(stdout, "1")
	case "submit":
		err = cfg.submit(args, stdout)
	case "connect":
		if len(args) != 1 {
			err = &agentError{"usage: duet connect <review link or id>"}
			break
		}
		err = cfg.textCmd("/api/agent/connect", map[string]any{"who": who(), "review": args[0]}, stdout)
	case "done":
		err = cfg.done(args, stdout)
	case "original":
		err = cfg.original(args, stdout)
	case "take":
		err = cfg.textCmd("/api/agent/take", map[string]any{"who": who()}, stdout)
	case "status":
		err = cfg.textCmd("/api/agent/status", map[string]any{"who": who()}, stdout)
	case "reviews":
		w := who()
		paths := make([]string, 0, len(args))
		for _, p := range args {
			paths = append(paths, abs(w.Cwd, p))
		}
		err = cfg.textCmd("/api/agent/reviews", agent.WebReq{Who: w, Paths: paths}, stdout)
	case "hook":
		return cfg.hook(args, stdout, stderr)
	case "wait":
		return cfg.wait(stderr)
	case "", "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	var ae *agentError
	if errors.As(err, &ae) {
		fmt.Fprintln(stderr, ae.text)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "duet:", err)
		return 1
	}
	return 0
}

// web — открыть новую вкладку: ревью или список. Всегда новую, даже если вкладка duet уже есть.
func (cfg Config) web(args []string, stdout io.Writer) error {
	w := who()
	q := agent.WebReq{Who: w}
	if len(args) > 0 && args[0] == "--files" {
		if len(args) < 2 || w.Session == "" {
			return errors.New("usage: duet web --files <task file>... (from an agent session)")
		}
		for _, p := range args[1:] {
			q.Paths = append(q.Paths, abs(w.Cwd, p))
		}
	} else {
		if len(args) > 1 {
			return errors.New("usage: duet web [review] | duet web --files <task file>...")
		}
		if len(args) == 1 {
			q.Review = args[0]
		}
	}
	port, err := cfg.Ensure()
	if err != nil {
		return err
	}
	link := URL(port) + "/"
	if w.Session != "" {
		var out struct{ Review string }
		if err := post(agentClient, port, "/api/agent/web", q, &out); err != nil {
			return err
		}
		q.Review = out.Review
	}
	if q.Review != "" {
		id := q.Review
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/reviews/%s", port, url.PathEscape(id)))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("no review %q", id)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("could not open review %q: HTTP %d", id, resp.StatusCode)
		}
		link = URL(port) + "/r/" + url.PathEscape(id)
	}
	fmt.Fprintln(stdout, link)
	if err := exec.Command(cfg.Browser, link).Run(); err != nil {
		return fmt.Errorf("could not open the browser (%s): %w; open the link yourself", cfg.Browser, err)
	}
	return nil
}

func (cfg Config) stopCmd(stdout io.Writer) error {
	in, ok := cfg.running()
	if !ok {
		fmt.Fprintln(stdout, "duet is not running")
		return nil
	}
	if err := cfg.stop(in); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "duet stopped")
	return nil
}
