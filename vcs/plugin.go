package vcs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Request и Reply — один запрос и один ответ на отдельный запуск процесса.
type Request struct {
	Protocol  int        `json:"protocol"`
	Operation string     `json:"operation"`
	Directory string     `json:"directory,omitempty"`
	Workspace *Workspace `json:"workspace,omitempty"`
	Revision  string     `json:"revision,omitempty"`
	Path      string     `json:"path,omitempty"`
}
type Reply struct {
	Protocol  int             `json:"protocol"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}
type Capabilities struct {
	Operations []string `json:"operations"`
}

type Executable struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

const MaxPluginOutput = 32 << 20
const PluginTimeout = 5 * time.Second

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, fmt.Errorf("plugin output exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(p)
}
func decode(data []byte, dst any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}
func (p Executable) call(ctx context.Context, q Request, dst any) error {
	if !filepath.IsAbs(p.Path) {
		return fmt.Errorf("plugin %s: executable path must be absolute", p.Name)
	}
	if _, ok := ctx.Deadline(); !ok || q.Operation == "hello" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, PluginTimeout)
		defer cancel()
	}
	q.Protocol = ProtocolVersion
	input, err := json.Marshal(q)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, p.Path)
	// Каталог запуска не влияет на поиск исполняемой программы.
	cmd.Dir = filepath.Dir(p.Path)
	deadline, _ := ctx.Deadline()
	cmd.Env = append(cmd.Environ(), "DUET_PLUGIN_TIMEOUT_MS="+strconv.FormatInt(max(0, time.Until(deadline).Milliseconds()), 10))
	cmd.Stdin = bytes.NewReader(input)
	out := &limitedBuffer{limit: MaxPluginOutput}
	stderr := &limitedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, stderr
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return fmt.Errorf("plugin %s (%s): %w", p.Name, q.Operation, err)
	}
	var reply Reply
	if err := decode(out.Bytes(), &reply); err != nil {
		return fmt.Errorf("plugin %s: invalid reply: %w", p.Name, err)
	}
	if reply.Protocol != ProtocolVersion {
		return fmt.Errorf("plugin %s: unsupported protocol %d (expected %d)", p.Name, reply.Protocol, ProtocolVersion)
	}
	if reply.ErrorCode == "timeout" && reply.Error != "" && len(reply.Result) == 0 {
		return fmt.Errorf("plugin %s (%s): %w", p.Name, q.Operation, context.DeadlineExceeded)
	}
	if reply.ErrorCode != "" {
		return fmt.Errorf("plugin %s: invalid error code %q", p.Name, reply.ErrorCode)
	}
	if reply.Error != "" {
		if len(reply.Result) != 0 {
			return fmt.Errorf("plugin %s: both result and error", p.Name)
		}
		return fmt.Errorf("plugin %s: %s", p.Name, reply.Error)
	}
	if len(reply.Result) == 0 {
		return fmt.Errorf("plugin %s: missing result", p.Name)
	}
	if err := decode(reply.Result, dst); err != nil {
		return fmt.Errorf("plugin %s: invalid result: %w", p.Name, err)
	}
	return nil
}
func (p Executable) Check(ctx context.Context) error {
	var c Capabilities
	if err := p.call(ctx, Request{Operation: "hello"}, &c); err != nil {
		return err
	}
	for _, required := range []string{"detect", "state", "original"} {
		found := false
		for _, op := range c.Operations {
			if op == required {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("plugin %s: missing operation %s", p.Name, required)
		}
	}
	return nil
}
func (p Executable) Detect(ctx context.Context, dir string) (*Workspace, error) {
	var w *Workspace
	if err := p.call(ctx, Request{Operation: "detect", Directory: dir}, &w); err != nil {
		return nil, err
	}
	if w == nil {
		return nil, nil
	}
	root, err := filepath.EvalSymlinks(w.Root)
	rel, relErr := filepath.Rel(w.Root, dir)
	if err != nil || relErr != nil || !filepath.IsAbs(w.Root) || root != w.Root || filepath.Clean(w.Root) != w.Root || w.ID == "" || w.Provider != p.Name || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("plugin %s: invalid workspace", p.Name)
	}
	return w, nil
}
func (p Executable) State(ctx context.Context, w Workspace) (State, error) {
	var s State
	// Указатели отличают обязательное пустое поле от пропущенного.
	var raw struct {
		Revision    *string      `json:"revision"`
		Token       string       `json:"token"`
		Changes     *[]Change    `json:"changes"`
		Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	}
	err := p.call(ctx, Request{Operation: "state", Workspace: &w}, &raw)
	if err != nil {
		return s, err
	}
	if raw.Revision == nil || raw.Changes == nil || raw.Token == "" {
		return s, fmt.Errorf("plugin %s: incomplete state", p.Name)
	}
	s = State{Revision: *raw.Revision, Token: raw.Token, Changes: *raw.Changes, Diagnostics: raw.Diagnostics}
	seen := map[string]bool{}
	for _, ch := range s.Changes {
		if !ValidPath(ch.Path) || seen[ch.Path] || ch.Status == "" || ch.PreviousPath != "" && !ValidPath(ch.PreviousPath) {
			return State{}, fmt.Errorf("plugin %s: invalid change", p.Name)
		}
		seen[ch.Path] = true
	}
	for _, d := range s.Diagnostics {
		if d.Code == "" || d.Message == "" || d.Path != "" && !ValidPath(d.Path) {
			return State{}, fmt.Errorf("plugin %s: invalid diagnostic", p.Name)
		}
	}
	return s, nil
}
func (p Executable) Original(ctx context.Context, w Workspace, revision, path string) (Content, error) {
	if !ValidPath(path) {
		return Content{}, fmt.Errorf("invalid original path")
	}
	var c Content
	if err := p.call(ctx, Request{Operation: "original", Workspace: &w, Revision: revision, Path: path}, &c); err != nil {
		return c, err
	}
	switch c.Kind {
	case "text":
		if (c.Mode != "100644" && c.Mode != "100755") || strings.ContainsRune(c.Text, 0) || c.Reason != "" {
			return Content{}, fmt.Errorf("plugin %s: invalid text content", p.Name)
		}
	case "absent":
		if c.Text != "" || c.Mode != "" || c.Reason != "" {
			return Content{}, fmt.Errorf("plugin %s: invalid absent content", p.Name)
		}
	case "unsupported":
		if c.Reason == "" || c.Text != "" {
			return Content{}, fmt.Errorf("plugin %s: missing unsupported reason", p.Name)
		}
	default:
		return Content{}, fmt.Errorf("plugin %s: invalid content kind", p.Name)
	}
	return c, nil
}
