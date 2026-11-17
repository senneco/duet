package agent

import (
	"strings"
	"testing"
)

func TestWebDoesNotReclaimKickedSession(t *testing.T) {
	f := setup(t, "claude")
	w := Who{Session: "s2", Agent: "claude", Cwd: f.dir}
	if _, err := f.s.Connect(w, f.r); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.Web(WebReq{Who: Who{Session: "s1", Cwd: f.dir}, Paths: []string{f.path}})
	if err == nil || !strings.Contains(err.Error(), "explicit handoff") {
		t.Fatal(err)
	}
	if se, _ := f.c.SessionInfo("s2"); se.Review != f.r {
		t.Fatal(se)
	}
}
