package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/senneco/duet/agent"
	"github.com/senneco/duet/core"
)

// Команды агента: `duet submit`, `duet done` и т. д. и хуки. Ответ — текст для агента
// ({text}) или ошибка, которую агент должен прочитать ({error: "agent", message}).

func (s *Server) agentRoutes() {
	m := s.mux
	post := func(path string, fn func(r *http.Request, dec func(any) error) (any, error)) {
		m.HandleFunc("POST /api/agent/"+path, func(w http.ResponseWriter, r *http.Request) {
			if s.Agent == nil {
				writeErr(w, http.StatusNotFound, "not-found", "no agent service", nil)
				return
			}
			dec := func(v any) error {
				err := json.NewDecoder(io.LimitReader(r.Body, 256<<20)).Decode(v)
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			out, err := fn(r, dec)
			var p *agent.Problem
			if errors.As(err, &p) {
				writeErr(w, http.StatusConflict, "agent", p.Text, nil)
				return
			}
			reply(w, out, err)
		})
	}
	text := func(t string, err error) (any, error) { return map[string]string{"text": t}, err }

	post("submit", func(r *http.Request, dec func(any) error) (any, error) {
		var q agent.SubmitReq
		if err := dec(&q); err != nil {
			return nil, err
		}
		return text(s.Agent.Submit(q))
	})
	post("connect", func(r *http.Request, dec func(any) error) (any, error) {
		var q struct {
			Who    agent.Who `json:"who"`
			Review string    `json:"review"`
		}
		if err := dec(&q); err != nil {
			return nil, err
		}
		return text(s.Agent.Connect(q.Who, q.Review))
	})
	post("done", func(r *http.Request, dec func(any) error) (any, error) {
		var q agent.DoneReq
		if err := dec(&q); err != nil {
			return nil, err
		}
		return text(s.Agent.Done(q))
	})
	post("original", func(r *http.Request, dec func(any) error) (any, error) {
		var q struct {
			Who      agent.Who     `json:"who"`
			Path     string        `json:"path"`
			Original core.Original `json:"original"`
		}
		if err := dec(&q); err != nil {
			return nil, err
		}
		return text(s.Agent.Original(q.Who, q.Path, q.Original))
	})
	who := func(fn func(*agent.Service, agent.Who) (string, error)) func(*http.Request, func(any) error) (any, error) {
		return func(r *http.Request, dec func(any) error) (any, error) {
			var q struct {
				Who agent.Who `json:"who"`
			}
			if err := dec(&q); err != nil {
				return nil, err
			}
			return text(fn(s.Agent, q.Who))
		}
	}
	post("take", who((*agent.Service).Take))
	post("status", who((*agent.Service).Status))
	post("reviews", func(r *http.Request, dec func(any) error) (any, error) {
		var q agent.WebReq
		if err := dec(&q); err != nil {
			return nil, err
		}
		return text(s.Agent.Reviews(q.Who, q.Paths), nil)
	})
	post("web", func(r *http.Request, dec func(any) error) (any, error) {
		var q agent.WebReq
		if err := dec(&q); err != nil {
			return nil, err
		}
		id, err := s.Agent.Web(q)
		return map[string]string{"review": id}, err
	})
	post("review-of", func(r *http.Request, dec func(any) error) (any, error) {
		var q struct {
			Session string `json:"session"`
		}
		if err := dec(&q); err != nil {
			return nil, err
		}
		return map[string]string{"review": s.Agent.ReviewOf(q.Session)}, nil
	})
	post("hook", func(r *http.Request, dec func(any) error) (any, error) {
		var q agent.HookReq
		if err := dec(&q); err != nil {
			return nil, err
		}
		return s.Agent.Hook(q), nil
	})
	// wait — долгий запрос фонового хука: ответ приходит, когда свободному агенту есть что передать.
	post("wait", func(r *http.Request, dec func(any) error) (any, error) {
		var q struct {
			Session string `json:"session"`
		}
		if err := dec(&q); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(s.ctx, cancel) // ядро останавливается — ждущий переподключится
		defer stop()
		t, again := s.Agent.Wait(ctx, q.Session)
		return map[string]any{"text": t, "again": again}, nil
	})
}
