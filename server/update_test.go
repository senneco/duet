package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestUpdateWaitsForEveryTabAndPreservesDraft(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "one"})
	before, _ := e.c.Review(id)
	e.post("/api/update/client", map[string]any{"id": "clean-tab", "ready": true})
	e.post("/api/update/client", map[string]any{"id": "draft-tab", "ready": false})
	result := make(chan int, 1)
	go func() { code, _ := e.post("/api/update/prepare", nil); result <- code }()
	var token string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, v := e.post("/api/update/client", map[string]any{"id": "clean-tab", "ready": true})
		token, _ = v["token"].(string)
		if token != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if token == "" {
		t.Fatal("no update handshake")
	}
	e.post("/api/update/client", map[string]any{"id": "clean-tab", "ack": token, "ready": true})
	e.post("/api/update/client", map[string]any{"id": "draft-tab", "ack": token, "ready": false})
	code, _ := e.post("/api/reviews/"+id+"/accept-file", map[string]string{"path": e.path("a.md")})
	if code != 503 {
		t.Fatal("write allowed during update", code)
	}
	if code := <-result; code != 409 {
		t.Fatal("draft did not prevent update", code)
	}
	after, _ := e.c.Review(id)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("review changed")
	}
}
func TestUpdateFreezeAndCancellation(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "one"})
	code, result := e.post("/api/update/prepare", nil)
	if code != 200 {
		t.Fatal(code, result)
	}
	token := result["token"].(string)
	code, _ = e.do("GET", "/api/reviews/"+id, nil, nil)
	if code != http.StatusServiceUnavailable {
		t.Fatal("core accessible while frozen")
	}
	e.post("/api/update/cancel", map[string]string{"token": token})
	code, _ = e.do("GET", "/api/reviews/"+id, nil, nil)
	if code != 200 {
		t.Fatal("cancel did not resume core")
	}
}
func TestBusyAgentPreventsUpdate(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "one"})
	if err := e.c.AgentStart(id); err != nil {
		t.Fatal(err)
	}
	code, _ := e.post("/api/update/prepare", nil)
	if code != 409 {
		t.Fatal("busy agent not protected", code)
	}
}
