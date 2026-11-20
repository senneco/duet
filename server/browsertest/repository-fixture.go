//go:build ignore

// Изолированный сервер для проверки полноты: go run server/browsertest/repository-fixture.go.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func write(path, text string) { must(os.WriteFile(path, []byte(text), 0600)) }
func git(root string, args ...string) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("git: %s %v", b, err))
	}
}
func main() {
	dir, err := os.MkdirTemp("", "duet-repository-browser-")
	must(err)
	defer os.RemoveAll(dir)
	dir, err = filepath.EvalSymlinks(dir)
	must(err)
	var mu sync.Mutex
	var c *core.Core
	var handler http.Handler
	var id, root, path, bin, other, otherID string
	http.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		home, e := os.MkdirTemp(dir, "case-")
		must(e)
		root = filepath.Join(home, "repo")
		must(os.MkdirAll(root, 0700))
		git(root, "init", "-q")
		git(root, "config", "user.name", "Test")
		git(root, "config", "user.email", "test@example.invalid")
		git(root, "config", "commit.gpgsign", "false")
		path = filepath.Join(root, "document.md")
		write(path, "# Документ\n\nИсходный текст.\n")
		git(root, "add", ".")
		git(root, "commit", "-qm", "fixture")
		write(path, "# Документ\n\nТекст для проверки.\n")
		c, e = core.Open(filepath.Join(home, "data"))
		must(e)
		id, _, e = c.CreateReview("Полнота репозитория", []core.Submission{{Path: path, Content: "# Документ\n\nТекст для проверки.\n", Original: &core.Original{Content: "Неверный исходник"}}})
		must(e)
		mode := r.URL.Query().Get("mode")
		if mode == "problems" || mode == "recover" {
			other = filepath.Join(root, "another-review.md")
			write(other, "Чужой материал")
			otherID, _, e = c.CreateReview("Другое ревью", []core.Submission{{Path: other, Content: "Чужой материал"}})
			must(e)
			_, e = c.AddComment(otherID, other, nil, "Сохранить комментарий", nil, false)
			must(e)
			must(c.AcceptFile(otherID, other))
			if mode == "recover" {
				_, e = c.PrepareWeb(core.Session{ID: "previous", Cwd: root}, otherID, []string{other})
				must(e)
				_, e = c.Stop("previous", false)
				must(e)
			}
			write(other, "Материал изменился после принятия")
		}
		if mode == "problems" {
			bin = filepath.Join(root, "image.bin")
			write(bin, "a\x00b")
			// Старый формат: исходник не позволяет доказать принятие при равенстве.
			data, e := os.ReadFile(filepath.Join(home, "data", "state.json"))
			must(e)
			var state map[string]any
			must(json.Unmarshal(data, &state))
			f := state["files"].(map[string]any)[path].(map[string]any)
			delete(f, "baseSource")
			delete(f, "baseHistory")
			data, e = json.Marshal(state)
			must(e)
			must(os.WriteFile(filepath.Join(home, "data", "state.json"), data, 0600))
			c, e = core.Open(filepath.Join(home, "data"))
			must(e)
		}
		_, e = c.PrepareWeb(core.Session{ID: "browser", Cwd: root}, id, []string{path})
		must(e)
		_, e = c.Stop("browser", false)
		must(e)
		handler = server.New(c, "repository-browser")
		json.NewEncoder(w).Encode(map[string]string{"id": id, "path": path, "bin": bin, "other": other, "otherID": otherID})
	})
	http.HandleFunc("/advance", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Query().Get("mode") {
		case "rollback":
			cm, e := c.AddComment(id, path, nil, "Вернуть исходный текст", nil, false)
			must(e)
			must(c.Send(id, false))
			c.Deliver("browser")
			write(path, "# Документ\n\nИсходный текст.\n")
			_, e = c.Done("browser", []core.Answer{{CommentID: cm, Text: "Вернул исходный текст"}})
			must(e)
		case "response":
			must(c.SetBusy("browser", true))
			write(path, "# Документ\n\nПервый ответ.\n")
			_, err = c.Done("browser", nil)
			must(err)
		case "continue":
			write(path, "# Документ\n\nВторой ответ.\n")
			_, err = c.Done("browser", nil)
			must(err)
			must(c.SetBusy("browser", false))
		case "late":
			write(filepath.Join(root, "late.md"), "# Поздний файл\n\nСоздан после проверки.\n")
		case "failure":
			must(os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "git-storage")))
			write(filepath.Join(root, ".git"), "gitdir: missing-storage\n")
			_, err = c.Stop("browser", false)
			must(err)
		case "fix":
			must(os.Remove(bin))
			git(root, "add", "another-review.md")
			git(root, "commit", "-qm", "resolve fixture conflict")
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := handler
		mu.Unlock()
		if h == nil {
			http.Error(w, "create fixture first", 404)
			return
		}
		h.ServeHTTP(w, r)
	})
	fmt.Println("http://127.0.0.1:4885")
	must(http.ListenAndServe("127.0.0.1:4885", nil))
}
