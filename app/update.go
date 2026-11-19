package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/senneco/duet/updates"
	"github.com/senneco/duet/vcs"
)

type updateStatus struct {
	Requested bool      `json:"requested,omitempty"`
	Phase     string    `json:"phase"`
	Version   string    `json:"version,omitempty"`
	Message   string    `json:"message,omitempty"`
	Checked   time.Time `json:"checked,omitempty"`
	Stage     string    `json:"stage,omitempty"`
}

func (cfg Config) updateStatePath() string { return filepath.Join(cfg.Home, "updates", "status.json") }
func (cfg Config) updateStatus() updateStatus {
	if _, err := updates.ReadConfig(cfg.Home); err != nil {
		return updateStatus{Phase: "disabled"}
	}
	var s updateStatus
	data, err := os.ReadFile(cfg.updateStatePath())
	if err != nil || json.Unmarshal(data, &s) != nil {
		return updateStatus{Phase: "idle"}
	}
	if s.Phase == "checking" || s.Phase == "downloading" || s.Phase == "building" || s.Phase == "applying" {
		if unlock, e := fileLock(filepath.Join(cfg.Home, "updates", "worker.lock"), syscall.LOCK_EX|syscall.LOCK_NB); e == nil {
			unlock()
			s.Phase = "error"
			s.Message = "The previous update was interrupted; try again"
		}
	}
	return s
}

// fileLock сериализует установку и запуск команд разных процессов.
func fileLock(path string, mode int) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), mode); err != nil {
		f.Close()
		return nil, err
	}
	return sync.OnceFunc(func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }), nil
}

func (cfg Config) update(args []string, out io.Writer) (err error) {
	check := len(args) == 1 && args[0] == "--check"
	if len(args) > 0 && !check {
		return errors.New("usage: duet update [--check]")
	}
	source, err := updates.ReadConfig(cfg.Home)
	if err != nil {
		return errors.New("updates are not configured; run the installation command once")
	}
	unlock, err := fileLock(filepath.Join(cfg.Home, "updates", "worker.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return errors.New("another update is already running")
	}
	defer unlock()
	state := cfg.updateStatus()
	state.Requested = !check
	set := func(phase, message string) {
		state.Phase, state.Message = phase, message
		_ = updates.WriteJSON(cfg.updateStatePath(), state)
		if out != nil {
			fmt.Fprintln(out, "duet:", phase, message)
		}
	}
	defer func() {
		if err != nil {
			set("error", err.Error())
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	b := updates.Builder{Config: source, Progress: func(p string) { set(p, "") }}
	set("checking", "")
	m, err := b.Check(ctx)
	state.Checked = time.Now()
	if err != nil {
		return err
	}
	if m.Version == source.Version {
		state.Stage = ""
		set("current", "")
		return nil
	}
	state.Version = m.Version
	if check {
		set("available", "")
		return nil
	}
	// Повторное нажатие применяет уже собранный и проверенный выпуск.
	var prepared updates.Manifest
	data, e := os.ReadFile(filepath.Join(state.Stage, "release.json"))
	if state.Stage == "" || e != nil || json.Unmarshal(data, &prepared) != nil || !sameManifest(prepared, m) {
		state.Stage, err = b.Prepare(ctx, cfg.Home, m)
		if err != nil {
			return err
		}
	}
	set("ready", "")
	if err = cfg.applyUpdate(state.Stage, source, m); err != nil {
		var busy *updateDeferred
		if errors.As(err, &busy) {
			set("ready", busy.Error())
			return nil
		}
		return err
	}
	state.Stage = ""
	set("current", "")
	return nil
}

func sameManifest(a, b updates.Manifest) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

type updateDeferred struct{ message string }

func (e *updateDeferred) Error() string { return e.message }

func (cfg Config) startUpdate() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(cfg.Home, "updates"), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(cfg.Home, "updates", "update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(exe, "update")
	cmd.Env = append(os.Environ(), "DUET_HOME="+cfg.Home, "DUET_PORT="+fmt.Sprint(cfg.Port))
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (cfg Config) checkUpdates(ctx context.Context) {
	check := func() {
		if _, err := updates.ReadConfig(cfg.Home); err != nil {
			return
		}
		s := cfg.updateStatus()
		if time.Since(s.Checked) < 24*time.Hour {
			return
		}
		_ = cfg.update([]string{"--check"}, io.Discard)
	}
	check()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

func (cfg Config) updateRequest(port int, path string, body any, out any) error {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/update/%s", port, path), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 404 {
			return &updateDeferred{"For this first upgrade, save drafts, finish agent work and stop the old duet, then run the installer again"}
		}
		var b struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		if b.Message == "" {
			b.Message = "Finish current work before applying the update"
		}
		return &updateDeferred{b.Message}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// savedFile сохраняет только изменяемые файлы установки. История ревью никогда
// не копируется назад: откат программы не должен откатывать действия пользователя.
type savedFile struct {
	Path    string
	Data    []byte
	Mode    os.FileMode
	Link    string
	Missing bool
}

type installationJournal struct {
	Saved   []savedFile
	Target  string
	Restart bool
	Port    int
}

func (cfg Config) journalPath() string {
	return filepath.Join(cfg.Home, "updates", "installation.json")
}

// При остановке процесса между заменами следующий запуск восстанавливает
// предыдущий комплект прежде, чем выполнять команды или запускать новое ядро.
func (cfg Config) recoverUpdate() error {
	if _, err := os.Stat(cfg.journalPath()); os.IsNotExist(err) {
		return nil
	}
	unlock, err := fileLock(filepath.Join(cfg.Home, "installation.lock"), syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	data, err := os.ReadFile(cfg.journalPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j installationJournal
	if err = json.Unmarshal(data, &j); err != nil {
		return errors.New("cannot read interrupted update journal")
	}
	if in, ok := cfg.running(); ok {
		var q struct {
			Token string `json:"token"`
		}
		if err = cfg.updateRequest(in.Port, "prepare", map[string]any{}, &q); err != nil {
			return err
		}
		if err = cfg.stop(in); err != nil {
			_ = cfg.updateRequest(in.Port, "cancel", map[string]string{"token": q.Token}, nil)
			return err
		}
	}
	if err = restore(j.Saved); err != nil {
		return fmt.Errorf("restore interrupted update: %w", err)
	}
	if j.Restart {
		cfg.Port = j.Port
		if err = cfg.startInstalled(j.Target); err != nil {
			return err
		}
	}
	return os.Remove(cfg.journalPath())
}

func snapshot(paths []string) ([]savedFile, error) {
	var saved []savedFile
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		f := savedFile{Path: p}
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			f.Missing = true
		} else if err != nil {
			return nil, err
		} else {
			f.Mode = st.Mode()
			if st.Mode()&os.ModeSymlink != 0 {
				f.Link, err = os.Readlink(p)
			} else if st.Mode().IsRegular() {
				f.Data, err = os.ReadFile(p)
			} else {
				return nil, fmt.Errorf("installation target is not a regular file: %s", p)
			}
			if err != nil {
				return nil, err
			}
		}
		saved = append(saved, f)
	}
	return saved, nil
}
func restore(saved []savedFile) error {
	var errs []error
	for _, f := range saved {
		var err error
		if f.Missing {
			err = os.Remove(f.Path)
			if os.IsNotExist(err) {
				err = nil
			}
		} else if f.Link != "" {
			_ = os.Remove(f.Path)
			err = os.Symlink(f.Link, f.Path)
		} else {
			err = replaceFile(f.Path, f.Data, f.Mode.Perm())
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func replaceFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".duet-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func installSettingPaths() []string {
	home, _ := os.UserHomeDir()
	claude, ok := os.LookupEnv("CLAUDE_CONFIG_DIR")
	if !ok {
		claude = filepath.Join(home, ".claude")
	}
	codex, ok := os.LookupEnv("CODEX_HOME")
	if !ok {
		codex = filepath.Join(home, ".codex")
	}
	var out []string
	for _, p := range []string{filepath.Join(claude, "skills/duet/SKILL.md"), filepath.Join(claude, "settings.json"), filepath.Join(codex, "skills/duet/SKILL.md"), filepath.Join(codex, "hooks.json"), filepath.Join(codex, "rules/duet.rules"), filepath.Join(codex, "config.toml")} {
		out = append(out, p, p+".duet-backup")
	}
	return out
}

func (cfg Config) applyUpdate(stage string, source updates.Config, m updates.Manifest) (err error) {
	if err = m.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bin := filepath.Join(stage, "duet")
	result, err := exec.CommandContext(ctx, bin, "--update-protocol").Output()
	if err != nil || string(bytes.TrimSpace(result)) != "1" {
		return errors.New("built duet does not support this update protocol")
	}
	for name, file := range m.Plugins {
		if err = (vcs.Executable{Name: name, Path: filepath.Join(stage, file)}).Check(ctx); err != nil {
			return err
		}
	}
	unlock, err := fileLock(filepath.Join(cfg.Home, "installation.lock"), syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	in, running := cfg.running()
	if running {
		var ready struct {
			Token string `json:"token"`
		}
		if err = cfg.updateRequest(in.Port, "prepare", map[string]any{}, &ready); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				_ = cfg.updateRequest(in.Port, "cancel", map[string]string{"token": ready.Token}, nil)
			}
		}()
		if err = cfg.stop(in); err != nil {
			return err
		}
		cfg.Port = in.Port
	}
	target := filepath.Join(source.BinDir, "duet")
	paths := append(installSettingPaths(), target, filepath.Join(cfg.Home, updates.ConfigFile))
	for _, f := range m.Files {
		paths = append(paths, filepath.Join(source.BinDir, f.Name))
	}
	for name := range m.Plugins {
		paths = append(paths, filepath.Join(cfg.Home, "plugins", name+".json"))
	}
	saved, err := snapshot(paths)
	if err != nil {
		if running {
			_ = cfg.startInstalled(target)
		}
		return err
	}
	defer func() {
		if err != nil {
			if cur, ok := cfg.running(); ok {
				_ = cfg.stop(cur)
			}
			if e := restore(saved); e != nil {
				err = errors.Join(err, fmt.Errorf("restore installation: %w", e))
				return
			}
			if running {
				if e := cfg.startInstalled(target); e != nil {
					err = errors.Join(err, fmt.Errorf("restart previous duet: %w", e))
					return
				}
			}
			_ = os.Remove(cfg.journalPath())
		}
	}()
	if err = updates.WriteJSON(cfg.journalPath(), installationJournal{Saved: saved, Target: target, Restart: running, Port: cfg.Port}); err != nil {
		return err
	}
	for _, f := range m.Files {
		data, e := os.ReadFile(filepath.Join(stage, f.Name))
		if e != nil {
			return e
		}
		if e = replaceFile(filepath.Join(source.BinDir, f.Name), data, 0755); e != nil {
			return e
		}
	}
	cmd := exec.Command(bin, "install", "--finish-update")
	cmd.Env = append(os.Environ(), "DUET_HOME="+cfg.Home, "DUET_BIN="+source.BinDir)
	if e := cmd.Run(); e != nil {
		return errors.New("cannot install the built duet; restoring previous installation")
	}
	for name, file := range m.Plugins {
		p := vcs.Executable{Name: name, Path: filepath.Join(source.BinDir, file)}
		if e := updates.WriteJSON(filepath.Join(cfg.Home, "plugins", name+".json"), p); e != nil {
			return e
		}
	}
	source.Version = m.Version
	if err = updates.WriteJSON(filepath.Join(cfg.Home, updates.ConfigFile), source); err != nil {
		return err
	}
	if running {
		if err = cfg.startInstalled(target); err != nil {
			return err
		}
	}
	return os.Remove(cfg.journalPath())
}

func (cfg Config) startInstalled(target string) error {
	f, err := os.OpenFile(cfg.logPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(target, "serve")
	cmd.Env = append(os.Environ(), "DUET_HOME="+cfg.Home, "DUET_PORT="+fmt.Sprint(cfg.Port), "DUET_UPDATE_CHILD=1")
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case e := <-done:
			if e == nil {
				e = errors.New("duet exited during startup")
			}
			return e
		default:
		}
		if in, ok := cfg.running(); ok && in.PID == cmd.Process.Pid {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	<-done
	return errors.New("new duet did not start")
}

// installSource — вход из bootstrap-скрипта после компиляции исходников.
func (cfg Config) installSource(path string) error {
	var source updates.Config
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &source); err != nil {
		return err
	}
	if err = source.Validate(); err != nil {
		return err
	}
	stage := filepath.Dir(path)
	var m updates.Manifest
	data, err = os.ReadFile(filepath.Join(stage, "release.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return err
	}
	return cfg.applyUpdate(stage, source, m)
}

func (cfg Config) installPublicRelease(manifest string) error {
	binDir := os.Getenv("DUET_BIN")
	if binDir == "" {
		home, _ := os.UserHomeDir()
		binDir = filepath.Join(home, ".local", "bin")
	}
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		return err
	}
	source := updates.Config{Protocol: 1, Manifest: updates.ManifestURL, BinDir: binDir}
	path := filepath.Join(filepath.Dir(manifest), updates.ConfigFile)
	if err = updates.WriteJSON(path, source); err != nil {
		return err
	}
	return cfg.installSource(path)
}
