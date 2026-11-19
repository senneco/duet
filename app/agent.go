package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/senneco/duet/agent"
	"github.com/senneco/duet/core"
)

// Команды, которые зовёт агент. Каждая начинается с Ensure и говорит с ядром по HTTP.
// Текст для агента — в stdout; ошибка, которую агент должен исправить, — в stderr с кодом 1.

// agentError — ядро ответило текстом для агента.
type agentError struct{ text string }

func (e *agentError) Error() string { return e.text }

var agentClient = &http.Client{Timeout: 60 * time.Second}

// call — POST к ядру; результат раскладывается в out.
func (cfg Config) call(cl *http.Client, path string, in, out any) error {
	port, err := cfg.Ensure()
	if err != nil {
		return err
	}
	return post(cl, port, path, in, out)
}

func post(cl *http.Client, port int, path string, in, out any) error {
	body, _ := json.Marshal(in)
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error, Message string }
		json.Unmarshal(data, &e)
		if e.Error == "agent" {
			return &agentError{e.Message}
		}
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(data))
		}
		return errors.New(e.Message)
	}
	return json.Unmarshal(data, out)
}

// who — сессия агента, из которой запущена команда.
func who() agent.Who {
	cwd, _ := os.Getwd()
	w := agent.Who{Cwd: agent.Canon(cwd)}
	switch {
	case os.Getenv("DUET_SESSION") != "":
		w.Session, w.Agent = os.Getenv("DUET_SESSION"), os.Getenv("DUET_AGENT")
		w.PID, _ = strconv.Atoi(os.Getenv("DUET_PID"))
	case os.Getenv("CLAUDE_CODE_SESSION_ID") != "":
		w.Session, w.Agent = os.Getenv("CLAUDE_CODE_SESSION_ID"), "claude"
		w.PID, _ = strconv.Atoi(os.Getenv("CLAUDE_PID"))
		if w.PID == 0 {
			w.PID = ancestor("claude")
		}
	case os.Getenv("CODEX_THREAD_ID") != "":
		w.Session, w.Agent = os.Getenv("CODEX_THREAD_ID"), "codex"
		w.PIDs = codexWindows(w.Cwd)
	}
	if h := os.Getenv("CODEX_HOME"); h != "" && w.Agent == "codex" {
		w.Env = map[string]string{"CODEX_HOME": h}
	}
	return w
}

// codexWindows — окна Codex (интерактивные `codex`), в которых может идти эта сессия.
// Команды агента Codex выполняет его общий фоновый процесс (app-server), он живёт и после
// закрытия окна, поэтому живость сессии — это живость окна. Сначала ищем окно среди родителей
// команды; если команду запустил фоновый процесс — окна Codex, запущенные в папке агента.
// Если таких несколько, сессия жива, пока жив хоть одно.
func codexWindows(cwd string) []int {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil
	}
	type proc struct {
		ppid int
		args string
	}
	procs := map[int]proc{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		procs[pid] = proc{ppid, strings.Join(f[2:], " ")}
	}
	window := func(args string) bool {
		f := strings.Fields(args)
		if len(f) == 0 || filepath.Base(f[0]) != "codex" {
			return false
		}
		for _, a := range f[1:] {
			switch a {
			case "app-server", "exec", "e", "queue", "mcp", "login", "agents":
				return false
			}
		}
		return true
	}
	for pid, n := os.Getppid(), 0; pid > 1 && n < 30; n++ {
		p, ok := procs[pid]
		if !ok {
			break
		}
		if window(p.args) {
			return []int{pid}
		}
		pid = p.ppid
	}
	var cands []string
	for pid, p := range procs {
		if window(p.args) {
			cands = append(cands, strconv.Itoa(pid))
		}
	}
	if len(cands) == 0 {
		return nil
	}
	lo, err := exec.Command("lsof", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(cands, ",")).Output()
	if err != nil && len(lo) == 0 {
		return nil
	}
	var pids []int
	pid := 0
	for _, line := range strings.Split(string(lo), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && agent.Canon(line[1:]) == cwd:
			pids = append(pids, pid)
		}
	}
	return pids
}

// ancestor — ближайший родительский процесс с таким именем; 0, если нет.
func ancestor(name string) int {
	pid := os.Getppid()
	for range 30 {
		if pid <= 1 {
			return 0
		}
		out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return 0
		}
		f := strings.Fields(string(out))
		if len(f) < 2 {
			return 0
		}
		if filepath.Base(strings.Join(f[1:], " ")) == name {
			return pid
		}
		pid, _ = strconv.Atoi(f[0])
	}
	return 0
}

func (cfg Config) textCmd(path string, in any, stdout io.Writer) error {
	var out struct{ Text string }
	if err := cfg.call(cfg.reviewClient(), path, in, &out); err != nil {
		return err
	}
	fmt.Fprintln(stdout, strings.TrimRight(out.Text, "\n"))
	return nil
}

// stdinData — то, что агент подал на вход (heredoc). Терминал — ничего.
func stdinData() ([]byte, error) {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice != 0 {
		return nil, nil
	}
	return io.ReadAll(os.Stdin)
}

type origSpec struct {
	Command string `json:"command"` // команда, которая печатает исходную версию (например, git show HEAD:path)
	File    string `json:"file"`    // файл с копией до правок
	Text    string `json:"text"`    // или сам текст
	Source  string `json:"source"`  // откуда взята: «git, HEAD»
}

// read — исходная версия как её описал агент.
func (o *origSpec) read(cwd string) (*core.Original, error) {
	if o == nil {
		return nil, nil
	}
	src := o.Source
	switch {
	case o.Command != "":
		cmd := exec.Command("sh", "-c", o.Command)
		cmd.Dir = cwd
		var errb bytes.Buffer
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			return nil, &agentError{fmt.Sprintf("duet: original command %q failed: %v %s\nIf the file did not exist before the task, send it without an original.", o.Command, err, strings.TrimSpace(errb.String()))}
		}
		if src == "" {
			src = o.Command
		}
		return &core.Original{Content: string(out), Source: src}, nil
	case o.File != "":
		b, err := os.ReadFile(abs(cwd, o.File))
		if err != nil {
			return nil, &agentError{"duet: cannot read the original copy: " + err.Error()}
		}
		if src == "" {
			src = o.File
		}
		return &core.Original{Content: string(b), Source: src}, nil
	}
	return &core.Original{Content: o.Text, Source: src}, nil
}

func abs(cwd, p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return agent.Canon(p)
}

func blocksAbs(cwd string, m map[string][]core.Block) map[string][]core.Block {
	if m == nil {
		return nil
	}
	out := map[string][]core.Block{}
	for p, b := range m {
		out[abs(cwd, p)] = b
	}
	return out
}

// submit — `duet submit [--new] [--title T] [--start F] [--note N] [file…]`, или JSON на входе.
func (cfg Config) submit(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	isNew := fs.Bool("new", false, "start a new review (new task)")
	title := fs.String("title", "", "review title")
	note := fs.String("note", "", "note for an agent that picks up the review")
	start := fs.String("start", "", "file the owner should read first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var m struct {
		Title string `json:"title"`
		Note  string `json:"note"`
		Start string `json:"start"`
		New   bool   `json:"new"`
		Files []struct {
			Path     string       `json:"path"`
			Deleted  bool         `json:"deleted"`
			Original *origSpec    `json:"original"`
			Blocks   []core.Block `json:"blocks"`
		} `json:"files"`
	}
	args = fs.Args()
	if len(args) == 1 && strings.HasPrefix(strings.TrimSpace(args[0]), "{") {
		// JSON одним аргументом: так команду пропускает правило Codex (вставку через heredoc он не разбирает).
		if err := json.Unmarshal([]byte(args[0]), &m); err != nil {
			return &agentError{"duet: bad JSON manifest: " + err.Error()}
		}
		args = nil
	} else if len(args) == 0 {
		data, err := stdinData()
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return &agentError{"duet: nothing to submit. Pass a JSON manifest as one argument, or file names (see the duet skill)."}
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return &agentError{"duet: bad JSON manifest: " + err.Error()}
		}
	}
	for _, p := range args {
		m.Files = append(m.Files, struct {
			Path     string       `json:"path"`
			Deleted  bool         `json:"deleted"`
			Original *origSpec    `json:"original"`
			Blocks   []core.Block `json:"blocks"`
		}{Path: p})
	}
	w := who()
	q := agent.SubmitReq{Who: w, Title: m.Title, Note: m.Note, New: m.New || *isNew}
	if *title != "" {
		q.Title = *title
	}
	if *note != "" {
		q.Note = *note
	}
	if *start != "" {
		m.Start = *start
	}
	if m.Start != "" {
		q.Start = abs(w.Cwd, m.Start)
	}
	for _, f := range m.Files {
		p := abs(w.Cwd, f.Path)
		sf := agent.SubmitFile{Path: p, Deleted: f.Deleted, Blocks: f.Blocks}
		if !f.Deleted {
			b, err := os.ReadFile(p)
			if errors.Is(err, os.ErrNotExist) {
				return &agentError{fmt.Sprintf("duet: %s does not exist. If you deleted it, send it with \"deleted\": true.", f.Path)}
			}
			if err != nil {
				return err
			}
			sf.Content = string(b)
		}
		o, err := f.Original.read(w.Cwd)
		if err != nil {
			return err
		}
		sf.Original = o
		q.Files = append(q.Files, sf)
	}
	return cfg.textCmd("/api/agent/submit", q, stdout)
}

// done — `duet done` с ответами на комментарии в JSON на входе.
func (cfg Config) done(args []string, stdout io.Writer) error {
	var m struct {
		Answers []struct {
			ID       string   `json:"id"`
			Text     string   `json:"text"`
			Question bool     `json:"question"`
			Options  []string `json:"options"`
			Anchor   string   `json:"anchor"`
		} `json:"answers"`
		Note   *string                 `json:"note"`
		Start  string                  `json:"start"`
		Blocks map[string][]core.Block `json:"blocks"`
	}
	data := []byte(strings.Join(args, " ")) // JSON одним аргументом
	if len(args) == 0 {
		var err error
		if data, err = stdinData(); err != nil {
			return err
		}
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			return &agentError{"duet: bad JSON: " + err.Error()}
		}
	}
	w := who()
	q := agent.DoneReq{Who: w, Note: m.Note, Blocks: blocksAbs(w.Cwd, m.Blocks)}
	if m.Start != "" {
		q.Start = abs(w.Cwd, m.Start)
	}
	for _, a := range m.Answers {
		q.Answers = append(q.Answers, core.Answer{CommentID: a.ID, Text: a.Text, Question: a.Question, Options: a.Options, AnchorText: a.Anchor})
	}
	return cfg.textCmd("/api/agent/done", q, stdout)
}

// original — `duet original <file> --source S (--command C | --file F | --text T)`.
func (cfg Config) original(args []string, stdout io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return &agentError{"usage: duet original <file> --source \"git, HEAD\" (--command \"git show HEAD:<file>\" | --file <copy> | --text <text>)"}
	}
	path := args[0]
	fs := flag.NewFlagSet("original", flag.ContinueOnError)
	var o origSpec
	fs.StringVar(&o.Source, "source", "", "where the original comes from")
	fs.StringVar(&o.Command, "command", "", "command that prints the original")
	fs.StringVar(&o.File, "file", "", "file with the original")
	fs.StringVar(&o.Text, "text", "", "the original text")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	w := who()
	orig, err := o.read(w.Cwd)
	if err != nil {
		return err
	}
	return cfg.textCmd("/api/agent/original", map[string]any{"who": w, "path": abs(w.Cwd, path), "original": orig}, stdout)
}

// hook — `duet hook claude|codex`: событие сессии агента со стандартного входа.
// Сессии, не подключённые к ревью, ядро не трогают: выход по отсутствию пометки.
func (cfg Config) hook(args []string, stdout, stderr io.Writer) int {
	data, _ := io.ReadAll(os.Stdin)
	var p struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(data, &p) != nil || p.SessionID == "" || !cfg.marked(p.SessionID) {
		return 0
	}
	ag := "claude"
	if len(args) > 0 {
		ag = args[0]
	}
	var out agent.HookResp
	if err := cfg.call(cfg.reviewClient(), "/api/agent/hook", agent.HookReq{Agent: ag, Payload: data}, &out); err != nil {
		return 0 // duet недоступен — агенту не мешаем
	}
	fmt.Fprint(stdout, out.Stdout)
	fmt.Fprint(stderr, out.Stderr)
	return out.Code
}

func (cfg Config) marked(session string) bool {
	if strings.ContainsAny(session, `/\`) || session == "." || session == ".." {
		return false
	}
	_, err := os.Stat(filepath.Join(cfg.Home, "sessions", session))
	return err == nil
}

// wait — `duet wait`: фоновый хук Claude Code на Stop. Ждёт, пока свободному агенту есть что
// передать, и будит его: текст в stderr, код 2. Ждать нечего — выходит с 0.
func (cfg Config) wait(stderr io.Writer) int {
	data, _ := io.ReadAll(os.Stdin)
	var p struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(data, &p) != nil || p.SessionID == "" {
		return 0
	}
	long := &http.Client{}
	for cfg.marked(p.SessionID) {
		var out struct {
			Text  string
			Again bool
		}
		err := cfg.call(long, "/api/agent/wait", map[string]string{"session": p.SessionID}, &out)
		switch {
		case err == nil && out.Text != "":
			fmt.Fprintln(stderr, out.Text)
			return 2
		case err == nil && !out.Again:
			return 0
		}
		time.Sleep(time.Second) // ядро перезапускается — подождать и переподключиться
	}
	return 0
}
