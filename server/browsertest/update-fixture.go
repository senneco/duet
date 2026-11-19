//go:build ignore

// Локальный стенд интерфейса обновлений с отдельными данными.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

func main() {
	dir, err := os.MkdirTemp("", "duet-update-browser-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "document.md")
	text := "# Обновления duet\n\nТекст для проверки чтения и черновика.\n\n"
	for i := 0; i < 60; i++ {
		text += fmt.Sprintf("Абзац %d. Материалы остаются на месте после обновления.\n\n", i)
	}
	os.WriteFile(path, []byte(text), 0600)
	c, err := core.Open(filepath.Join(dir, "data"))
	if err != nil {
		panic(err)
	}
	id, _, err := c.CreateReview("Обновление из исходников", []core.Submission{{Path: path, Content: text, Original: &core.Original{Source: "fixture", Content: text}}})
	if err != nil {
		panic(err)
	}
	s := server.New(c, "update-test-1")
	var mu sync.Mutex
	phase := "available"
	s.UpdateStatus = func() any {
		mu.Lock()
		defer mu.Unlock()
		return map[string]string{"phase": phase, "version": "test-release"}
	}
	s.StartUpdate = func() error { mu.Lock(); phase = "building"; mu.Unlock(); return nil }
	http.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Query().Get("phase"); p != "" {
			mu.Lock()
			phase = p
			mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "path": path})
	})
	http.Handle("/", s)
	fmt.Println("http://127.0.0.1:4896/r/" + id)
	if err = http.ListenAndServe("127.0.0.1:4896", nil); err != nil {
		panic(err)
	}
}
