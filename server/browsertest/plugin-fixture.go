//go:build ignore

// go run server/browsertest/plugin-fixture.go /absolute/path/to/example-git
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
	"github.com/senneco/duet/vcs"
)

func main() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	dir, err := os.MkdirTemp("", "duet-plugin-browser-")
	must(err)
	defer os.RemoveAll(dir)
	dir, err = filepath.EvalSymlinks(dir)
	must(err)
	root := filepath.Join(dir, "repo")
	must(os.Mkdir(root, 0700))
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			panic(fmt.Sprintf("%s: %v", out, err))
		}
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	git("config", "commit.gpgsign", "false")
	path := filepath.Join(root, "document.md")
	must(os.WriteFile(path, []byte("Исходный текст\n"), 0600))
	git("add", ".")
	git("commit", "-qm", "fixture")
	must(os.WriteFile(path, []byte("Новый текст\n"), 0600))
	bin := filepath.Join(dir, "example-git")
	data, err := os.ReadFile(os.Args[1])
	must(err)
	must(os.WriteFile(bin, data, 0700))
	home := filepath.Join(dir, "data")
	registry := vcs.Registry{Home: home}
	must(registry.Add(context.Background(), vcs.Executable{Name: "example-git", Path: bin}))
	c, err := core.Open(home)
	must(err)
	id, _, err := c.CreateReview("Проверка внешнего плагина", []core.Submission{{Path: path, Content: "Новый текст\n"}})
	must(err)
	_, err = c.PrepareWeb(core.Session{ID: "browser", Cwd: root}, id, []string{path})
	must(err)
	_, err = c.Stop("browser", false)
	must(err)
	var mu sync.Mutex
	http.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"id": id}) })
	http.HandleFunc("/plugin", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("broken") == "1" {
			must(os.Rename(bin, bin+".off"))
		} else {
			must(os.Rename(bin+".off", bin))
		}
		w.WriteHeader(204)
	})
	http.Handle("/", server.New(c, "plugin-browser"))
	fmt.Println("http://127.0.0.1:4886/r/" + id)
	must(http.ListenAndServe("127.0.0.1:4886", nil))
}
