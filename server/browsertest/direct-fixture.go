//go:build ignore

// Изолированный сервер: go run server/browsertest/direct-fixture.go
package main

import (
	"encoding/json"
	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
	"net/http"
	"os"
)

func main() {
	dir, _ := os.MkdirTemp("", "duet-direct-browser-")
	c, e := core.Open(dir)
	if e != nil {
		panic(e)
	}
	s := server.New(c, "direct-test")
	http.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		dir, e := os.MkdirTemp(dir, "review-")
		if e != nil {
			panic(e)
		}
		p := dir + "/accepted.md"
		old := "# Принятый документ\n\nИсходный текст.\n"
		next := "# Принятый документ\n\nИсправленный текст.\n"
		os.WriteFile(p, []byte(old), 0600)
		id, _, e := c.CreateReview("Прямой ответ", []core.Submission{{Path: p, Content: old, Original: &core.Original{Content: old}}})
		if e != nil {
			panic(e)
		}
		if e = c.AcceptFile(id, p); e != nil {
			panic(e)
		}
		if r.URL.Query().Has("new") {
			if e = c.AgentStart(id); e != nil {
				panic(e)
			}
			files := []core.Submission{{Path: dir + "/first.md", Content: "# Первый новый файл\n\nПервый текст.\n"}, {Path: dir + "/second.md", Content: "# Второй новый файл\n\nВторой текст.\n"}}
			for _, f := range files {
				if e = os.WriteFile(f.Path, []byte(f.Content), 0600); e != nil {
					panic(e)
				}
			}
			if _, e = c.AgentFinish(id, files, nil); e != nil {
				panic(e)
			}
			json.NewEncoder(w).Encode(map[string]string{"id": id, "first": files[0].Path, "second": files[1].Path})
			return
		}
		cm, e := c.AddComment(id, p, nil, "Уточни текст", nil, false)
		if e != nil {
			panic(e)
		}
		if e = c.Send(id, false); e != nil {
			panic(e)
		}
		if _, e = c.Take(id); e != nil {
			panic(e)
		}
		os.WriteFile(p, []byte(next), 0600)
		if _, e = c.AgentFinish(id, []core.Submission{{Path: p, Content: next}}, []core.Answer{{CommentID: cm, Text: "Текст уточнён"}}); e != nil {
			panic(e)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "path": p})
	})
	http.Handle("/", s)
	panic(http.ListenAndServe("127.0.0.1:4884", nil))
}
