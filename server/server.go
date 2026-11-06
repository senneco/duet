// Package server отдаёт ядро клиентам по HTTP: состояние, отличия, действия владельца
// и поток событий об изменениях. Слушает только эту машину.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/senneco/duet/agent"
	"github.com/senneco/duet/core"
)

//go:embed web
var webFS embed.FS

// Server — HTTP-обработчик поверх ядра.
type Server struct {
	core  *core.Core
	build string
	mux   *http.ServeMux
	// ctx закрывается при остановке: потоки событий завершаются.
	ctx    context.Context
	cancel context.CancelFunc
	// OnShutdown вызывается по запросу остановки (`duet stop`), после ответа клиенту.
	OnShutdown func()
	// Agent — команды и хуки агентов; nil — их нет (тесты клиента).
	Agent *agent.Service

	mu   sync.Mutex
	subs map[chan event]bool
}

type event struct {
	name string
	data any
}

// New — сервер над ядром. build — отпечаток сборки: клиент перезагружается, когда он поменялся.
func New(c *core.Core, build string) *Server {
	s := &Server{core: c, build: build, mux: http.NewServeMux(), subs: map[chan event]bool{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.routes()
	go s.relay()
	return s
}

// Close завершает потоки событий.
func (s *Server) Close() { s.cancel() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !localHost(r.Host) {
		// Чужое имя — например, сайт, который подменил адрес своего домена на эту машину.
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if o := r.Header.Get("Origin"); o != "" && !localOrigin(o) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Чужой сайт не может прислать JSON без предварительного запроса, а на него мы не отвечаем.
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
	}
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

// localHost — запрос пришёл на одно из имён этой машины.
func localHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	switch strings.ToLower(h) {
	case "duet.localhost", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func localOrigin(o string) bool {
	rest, ok := strings.CutPrefix(o, "http://")
	return ok && localHost(rest)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"build": s.build})
	})
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("GET /api/reviews", func(w http.ResponseWriter, r *http.Request) {
		list := s.core.Reviews()
		for i := range list {
			list[i] = summary(list[i])
		}
		writeJSON(w, nonNil(list))
	})
	m.HandleFunc("GET /api/reviews/{id}", func(w http.ResponseWriter, r *http.Request) {
		rv, err := s.core.Review(r.PathValue("id"))
		reply(w, summary(rv), err)
	})
	m.HandleFunc("GET /api/reviews/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		fv, err := s.core.File(r.PathValue("id"), r.URL.Query().Get("path"))
		reply(w, fv, err)
	})
	m.HandleFunc("GET /api/reviews/{id}/diff", func(w http.ResponseWriter, r *http.Request) {
		id, path := r.PathValue("id"), r.URL.Query().Get("path")
		var ch []core.Chunk
		var err error
		switch mode := r.URL.Query().Get("mode"); mode {
		case "", "base":
			ch, err = s.core.FileDiff(id, path)
		case "response":
			ch, err = s.core.ResponseDiff(id, path)
		case "disk":
			ch, err = s.core.DiskDiff(id, path)
		default:
			writeErr(w, http.StatusBadRequest, "bad-request", "unknown mode "+mode, nil)
			return
		}
		reply(w, nonNil(ch), err)
	})
	m.HandleFunc("GET /api/reviews/{id}/responses", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.core.Responses(r.PathValue("id"))
		reply(w, nonNil(h), err)
	})
	m.HandleFunc("POST /api/reviews/{id}/{action}", s.action)
	s.agentRoutes()
	s.imageRoutes()
	m.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if s.OnShutdown != nil {
			go s.OnShutdown()
		}
	})

	web, _ := fs.Sub(webFS, "web")
	index := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, web, "index.html")
	}
	m.HandleFunc("GET /{$}", index)
	m.HandleFunc("GET /r/{id}", index)
	// Клиент: модули и стили. Каждый раз сверяются с ядром: новая сборка — новые файлы.
	files := http.StripPrefix("/web/", http.FileServerFS(web))
	m.HandleFunc("GET /web/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// summary — ревью без содержимого файлов: для списка и дерева. Содержимое — в /file.
func summary(rv core.ReviewView) core.ReviewView {
	files := make([]core.FileView, len(rv.Files))
	for i, f := range rv.Files {
		f.Base, f.Current, f.Disk, f.Response = "", "", "", nil
		f.Comments = nil
		files[i] = f
	}
	rv.Files = files
	return rv
}

// body — тело действия. Все поля необязательные, каждое действие берёт свои.
type body struct {
	Path       string      `json:"path"`
	Paths      []string    `json:"paths"`
	Content    string      `json:"content"`
	On         bool        `json:"on"`
	ID         string      `json:"id"`
	Text       string      `json:"text"`
	Images     []string    `json:"images"`
	Range      *core.Range `json:"range"`
	InResponse bool        `json:"inResponse"`
	// Was — комментарий к удалённому тексту: что было; Range — точка, где это было.
	Was string `json:"was"`
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b body
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "bad-request", err.Error(), nil)
		return
	}
	c := s.core
	var err error
	var out any = map[string]bool{"ok": true}
	switch r.PathValue("action") {
	case "sync":
		err = s.sync(id)
	case "opened":
		err = c.MarkOpened(id, b.Path)
	case "accept-file":
		err = c.AcceptFile(id, b.Path)
	case "edit":
		err = c.Edit(id, b.Path, b.Content)
	case "editing":
		err = c.SetEditing(id, b.On)
	case "revert":
		err = c.RevertToBase(id, b.Path)
	case "accept-response":
		err = c.AcceptResponse(id)
	case "reject-response":
		err = c.RejectResponse(id)
	case "take-from-disk":
		err = c.TakeFromDisk(id, b.Paths)
	case "overwrite":
		err = c.Overwrite(id, b.Paths)
	case "interrupt":
		err = c.Interrupt(id)
	case "restore-anyway":
		err = c.RestoreAnyway(id)
	case "send":
		err = c.Send(id)
	case "comment":
		var cid string
		if b.Was != "" && b.Range != nil {
			cid, err = c.AddRemovedComment(id, b.Path, b.Range.Start, b.Was, b.Text, b.Images)
		} else {
			cid, err = c.AddComment(id, b.Path, b.Range, b.Text, b.Images, b.InResponse)
		}
		out = map[string]string{"id": cid}
	case "comment-edit":
		err = c.EditComment(id, b.ID, b.Text, b.Images)
	case "comment-delete":
		err = c.DeleteComment(id, b.ID)
	case "reply":
		err = c.Reply(id, b.ID, b.Text, b.Images)
	case "dismiss":
		err = c.DismissQuestion(id, b.ID)
	default:
		writeErr(w, http.StatusNotFound, "not-found", "unknown action", nil)
		return
	}
	reply(w, out, err)
}

// sync сверяет ревью с диском и рассылает прогресс. Сообщения о прогрессе — не чаще 20 в секунду.
func (s *Server) sync(id string) error {
	var last time.Time
	n := 0
	err := s.core.Sync(id, func(done, total int) {
		n = total
		if done < total && time.Since(last) < 50*time.Millisecond {
			return
		}
		last = time.Now()
		s.broadcast(event{"sync", map[string]any{"review": id, "done": done, "total": total}})
	})
	// Сверять было нечего (например, ревью принято целиком) — всё равно сообщаем, что сверка закончена.
	if err == nil && n == 0 {
		s.broadcast(event{"sync", map[string]any{"review": id, "done": 0, "total": 0}})
	}
	return err
}

// relay пересылает изменения ядра всем открытым потокам событий.
func (s *Server) relay() {
	sub := s.core.Subscribe()
	defer sub.Close()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-sub.Wake():
			if ids := sub.Take(); len(ids) > 0 {
				s.broadcast(event{"change", map[string]any{"reviews": ids}})
			}
		}
	}
}

func (s *Server) broadcast(e event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- e:
		default:
			// Вкладка не успевает читать: пусть перечитает всё.
			select {
			case ch <- event{"reset", map[string]any{}}:
			default:
			}
		}
	}
}

// events — поток событий (Server-Sent Events). Первым приходит hello с отпечатком сборки.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan event, 256)
	s.mu.Lock()
	s.subs[ch] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	send := func(e event) bool {
		data, _ := json.Marshal(e.data)
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, data)
		fl.Flush()
		return err == nil
	}
	// retry — через сколько браузер переподключится, если duet остановлен.
	fmt.Fprint(w, "retry: 1000\n\n")
	if !send(event{"hello", map[string]string{"build": s.build}}) {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case e := <-ch:
			if !send(e) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		status, code := classify(err)
		var paths []string
		var dc *core.DiskChangedError
		if errors.As(err, &dc) {
			paths = dc.Paths
		}
		writeErr(w, status, code, err.Error(), paths)
		return
	}
	writeJSON(w, v)
}

// Ошибка для клиента: код (по нему клиент выбирает текст на своём языке), сообщение ядра и файлы.
type apiError struct {
	Error   string   `json:"error"`
	Message string   `json:"message"`
	Paths   []string `json:"paths,omitempty"`
}

func writeErr(w http.ResponseWriter, status int, code, msg string, paths []string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiError{code, msg, paths})
}

var codes = []struct {
	err  error
	code string
}{
	{core.ErrNotFound, "not-found"},
	{core.ErrUnviewedResponse, "unviewed-response"},
	{core.ErrAgentWorking, "agent-working"},
	{core.ErrAgentNotWorking, "agent-not-working"},
	{core.ErrNoResponse, "no-response"},
	{core.ErrMoved, "moved"},
	{core.ErrEditing, "editing"},
	{core.ErrDiskChanged, "disk-changed"},
	{core.ErrStopping, "stopping"},
	{core.ErrNoAgent, "no-agent"},
	{core.ErrCommentStatus, "comment-status"},
	{core.ErrBadRange, "bad-range"},
	{core.ErrNotInResponse, "not-in-response"},
	{core.ErrNothingToSend, "nothing-to-send"},
	{core.ErrNothingRunning, "nothing-running"},
	{core.ErrOtherAgent, "other-agent"},
	{core.ErrNotConnected, "not-connected"},
}

func classify(err error) (int, string) {
	for _, c := range codes {
		if errors.Is(err, c.err) {
			if c.code == "not-found" {
				return http.StatusNotFound, c.code
			}
			return http.StatusConflict, c.code
		}
	}
	return http.StatusConflict, "error"
}
