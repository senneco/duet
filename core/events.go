package core

import (
	"sort"
	"sync"
)

// Subscription — подписка на изменения: какие ревью поменялись с прошлого Take.
// Изменения копятся, пока подписчик их не забрал, поэтому медленный подписчик ничего не теряет
// и никого не тормозит.
type Subscription struct {
	c    *Core
	wake chan struct{}
	ids  map[string]bool
}

// Subscribe — подписаться на изменения ревью. Закрывать через Close.
func (c *Core) Subscribe() *Subscription {
	s := &Subscription{c: c, wake: make(chan struct{}, 1), ids: map[string]bool{}}
	c.subMu.Lock()
	c.subs[s] = true
	c.subMu.Unlock()
	return s
}

// Wake — сигнал «есть изменения». После сигнала забрать их через Take.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Take — ревью, которые поменялись с прошлого вызова, по порядку.
func (s *Subscription) Take() []string {
	s.c.subMu.Lock()
	defer s.c.subMu.Unlock()
	out := make([]string, 0, len(s.ids))
	for id := range s.ids {
		out = append(out, id)
	}
	sort.Strings(out)
	s.ids = map[string]bool{}
	return out
}

func (s *Subscription) Close() {
	s.c.subMu.Lock()
	delete(s.c.subs, s)
	s.c.subMu.Unlock()
}

// changed — в ревью что-то поменялось, что видно клиенту.
func (c *Core) changed(id string) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for s := range c.subs {
		s.ids[id] = true
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// subsState — часть Core для подписок; отдельный замок, чтобы сообщать об изменениях под c.mu.
type subsState struct {
	subMu sync.Mutex
	subs  map[*Subscription]bool
}
