// Package agent — протокол агента поверх ядра: команды duet, которые зовёт агент, его хуки,
// пробуждение свободного агента и проверка, жив ли он. Поведение — docs/specs/agent-protocol.md.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/senneco/duet/core"
)

func errorsIs(err, target error) bool     { return errors.Is(err, target) }
func errorsAs(err error, target any) bool { return errors.As(err, target) }

// Service — сторона ядра: хранит, кому что передать, будит агентов, следит за живостью.
type Service struct {
	core *core.Core
	// Home — папка данных duet: в Home/sessions лежат пометки подключённых сессий,
	// по ним хуки в остальных сессиях сразу выходят и ядро не трогают.
	Home string
	// URL — адрес ревью для владельца.
	URL func(review string) string
	// Queue — передать сообщение свободной сессии Codex (по умолчанию `codex queue`).
	Queue func(s core.Session, text string) error
	// Alive — жив ли процесс агента (по умолчанию — сигнал 0).
	Alive func(pid int) bool

	mu      sync.Mutex
	waiters map[string]chan struct{} // сессия → ждущий `duet wait`; новый сменяет старого
}

func New(c *core.Core, home string, url func(string) string) *Service {
	s := &Service{core: c, Home: home, URL: url, Queue: codexQueue, Alive: alive, waiters: map[string]chan struct{}{}}
	s.syncMarkers()
	return s
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func codexQueue(s core.Session, text string) error {
	cmd := exec.Command("codex", "queue", "--thread", s.ID, "--message", text)
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("codex queue: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *Service) markerDir() string { return filepath.Join(s.Home, "sessions") }

// syncMarkers — пометки на диске совпадают с сессиями, о которых знает ядро.
func (s *Service) syncMarkers() {
	dir := s.markerDir()
	os.MkdirAll(dir, 0o755)
	want := map[string]bool{}
	for _, se := range s.core.Sessions() {
		want[se.ID] = true
	}
	entries, _ := os.ReadDir(dir)
	have := map[string]bool{}
	for _, e := range entries {
		if !want[e.Name()] {
			os.Remove(filepath.Join(dir, e.Name()))
		}
		have[e.Name()] = true
	}
	for id := range want {
		if !have[id] && safeID(id) {
			os.WriteFile(filepath.Join(dir, id), nil, 0o644)
		}
	}
}

// safeID — номер сессии годится для имени файла.
func safeID(id string) bool {
	if id == "" || len(id) > 200 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') || id == "." || id == ".." {
			return false
		}
	}
	return true
}

// Run — фоновая работа: живость сессий и доставка сообщений свободным сессиям Codex.
func (s *Service) Run(ctx context.Context) {
	sub := s.core.Subscribe()
	defer sub.Close()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.checkAlive()
			s.deliverCodex()
		case <-sub.Wake():
			sub.Take()
			s.deliverCodex()
			s.wakeWaiters()
		}
	}
}

// checkAlive — процесс агента пропал (закрыто окно, убит): сессия отключается.
func (s *Service) checkAlive() {
	changed := false
	for _, se := range s.core.Sessions() {
		if len(se.PIDs) > 0 && !s.anyAlive(se.PIDs) {
			if err := s.core.Detach(se.ID); err != nil {
				log.Printf("detach %s: %v", se.ID, err)
			}
			changed = true
		}
	}
	if changed {
		s.syncMarkers()
	}
}

func (s *Service) anyAlive(pids []int) bool {
	for _, p := range pids {
		if s.Alive(p) {
			return true
		}
	}
	return false
}

// deliverCodex — свободная сессия Codex получает комментарии и сообщения очередью сессии.
func (s *Service) deliverCodex() {
	for _, se := range s.core.Sessions() {
		if se.Agent != "codex" || se.Review == "" || s.core.Busy(se.ID) || !s.core.Pending(se.ID) {
			continue
		}
		cms, ns, err := s.core.Deliver(se.ID)
		if err != nil || (len(cms) == 0 && len(ns) == 0) {
			continue
		}
		s.core.SetBusy(se.ID, true) // сообщение начнёт ход агента
		if err := s.Queue(se, delivery(se.Cwd, cms, ns)); err != nil {
			log.Printf("wake codex %s: %v", se.ID, err)
			s.core.SetBusy(se.ID, false)
		}
	}
}

func (s *Service) wakeWaiters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Wait — `duet wait` из фонового хука Claude Code: ждёт, пока свободной сессии есть что передать.
// Возвращает текст для агента; "" — ждать больше нечего (сессия отключена, пришёл новый ждущий).
// again — ядро останавливается: ждущему стоит переподключиться.
func (s *Service) Wait(ctx context.Context, session string) (text string, again bool) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if old, ok := s.waiters[session]; ok {
		close(old)
	}
	s.waiters[session] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.waiters[session] == ch {
			delete(s.waiters, session)
		}
		s.mu.Unlock()
	}()
	for {
		se, ok := s.core.SessionInfo(session)
		if !ok || se.Review == "" {
			return "", false
		}
		if !s.core.Busy(session) && s.core.Pending(session) {
			cms, ns, err := s.core.Deliver(session)
			if err == nil && (len(cms) > 0 || len(ns) > 0) {
				if len(cms) == 0 {
					s.core.SetBusy(session, true)
				}
				return delivery(se.Cwd, cms, ns), false
			}
		}
		select {
		case <-ctx.Done():
			return "", true
		case _, open := <-ch:
			if !open {
				return "", false // сменил новый ждущий
			}
		case <-time.After(5 * time.Second):
		}
	}
}
