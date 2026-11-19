package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type updateClient struct {
	Ack   string
	Ready bool
}
type updateState struct {
	gate    sync.RWMutex
	mu      sync.Mutex
	clients map[string]updateClient
	token   string
	release func()
}

// updateAllowed удерживает обычный запрос до завершения обработчика. Долгое
// ожидание агента и SSE не держат gate; при остановке они закрываются сервером.
func (s *Server) updateAllowed(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if strings.HasPrefix(r.URL.Path, "/api/update/") || r.URL.Path == "/api/health" || r.URL.Path == "/api/shutdown" || r.URL.Path == "/api/events" || r.URL.Path == "/api/agent/wait" || !strings.HasPrefix(r.URL.Path, "/api/") {
		return func() {}, true
	}
	s.updates.gate.RLock()
	s.updates.mu.Lock()
	paused := s.updates.token != ""
	s.updates.mu.Unlock()
	if paused {
		s.updates.gate.RUnlock()
		writeErr(w, http.StatusServiceUnavailable, "updating", "Update in progress; try again shortly", nil)
		return nil, false
	}
	return s.updates.gate.RUnlock, true
}

func (s *Server) cancelUpdate(token string) {
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	if s.updates.token != token {
		return
	}
	if s.updates.release != nil {
		s.updates.release()
		s.updates.release = nil
	}
	s.updates.token = ""
}

func (s *Server) updateRoutes() {
	s.updates.clients = map[string]updateClient{}
	s.mux.HandleFunc("GET /api/update/status", func(w http.ResponseWriter, r *http.Request) {
		if s.UpdateStatus == nil {
			writeJSON(w, map[string]string{"phase": "disabled"})
			return
		}
		writeJSON(w, s.UpdateStatus())
	})
	s.mux.HandleFunc("POST /api/update/start", func(w http.ResponseWriter, r *http.Request) {
		if s.StartUpdate == nil {
			writeErr(w, 409, "updates-disabled", "Updates are not configured", nil)
			return
		}
		if err := s.StartUpdate(); err != nil {
			writeErr(w, 409, "update", err.Error(), nil)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	s.mux.HandleFunc("POST /api/update/client", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ID    string `json:"id"`
			Ack   string `json:"ack"`
			Ready bool   `json:"ready"`
			Leave bool   `json:"leave"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil || len(b.ID) < 8 || len(b.ID) > 80 {
			writeErr(w, 400, "bad-request", "Invalid client", nil)
			return
		}
		s.updates.mu.Lock()
		defer s.updates.mu.Unlock()
		if b.Leave {
			delete(s.updates.clients, b.ID)
		} else {
			s.updates.clients[b.ID] = updateClient{Ack: b.Ack, Ready: b.Ready}
		}
		writeJSON(w, map[string]string{"token": s.updates.token})
	})
	s.mux.HandleFunc("POST /api/update/cancel", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Token string `json:"token"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil {
			writeErr(w, 400, "bad-request", "Invalid token", nil)
			return
		}
		s.cancelUpdate(b.Token)
		writeJSON(w, map[string]bool{"ok": true})
	})
	s.mux.HandleFunc("POST /api/update/prepare", func(w http.ResponseWriter, r *http.Request) {
		token := fmt.Sprint(time.Now().UnixNano())
		s.updates.gate.Lock()
		s.updates.mu.Lock()
		if s.updates.token != "" {
			s.updates.mu.Unlock()
			s.updates.gate.Unlock()
			writeErr(w, 409, "update-busy", "Another update is applying", nil)
			return
		}
		s.updates.token = token
		s.updates.mu.Unlock()
		s.updates.gate.Unlock()
		// Если обновляющий процесс исчезнет, работа возобновится сама.
		time.AfterFunc(30*time.Second, func() { s.cancelUpdate(token) })
		deadline := time.Now().Add(4 * time.Second)
		for {
			s.updates.mu.Lock()
			ready := true
			for _, c := range s.updates.clients {
				if c.Ack != token || !c.Ready {
					ready = false
					break
				}
			}
			if ready {
				unlock, err := s.core.FreezeForUpdate()
				if err == nil {
					s.updates.release = unlock
					s.updates.mu.Unlock()
					writeJSON(w, map[string]string{"token": token})
					return
				}
				s.updates.mu.Unlock()
				s.cancelUpdate(token)
				writeErr(w, 409, "update-not-ready", err.Error(), nil)
				return
			}
			s.updates.mu.Unlock()
			if time.Now().After(deadline) || r.Context().Err() != nil {
				s.cancelUpdate(token)
				writeErr(w, 409, "update-not-ready", "Save drafts and open or close the other duet tabs, then apply again", nil)
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	})
}
