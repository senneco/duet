//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
)

func main() {
	dir, _ := os.MkdirTemp("", "duet-count-")
	c, err := core.Open(dir)
	if err != nil {
		panic(err)
	}
	http.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		p := dir + "/spec.md"
		text := ""
		for i := 0; i < 60; i++ {
			text += fmt.Sprintf("## Раздел %d\n\nТекст раздела для чтения.\n\n", i)
		}
		os.WriteFile(p, []byte(text), 0600)
		id, _, err := c.CreateReview("Счётчик комментариев", []core.Submission{{Path: p, Content: text}})
		if err != nil {
			panic(err)
		}
		ids := []string{}
		for _, n := range []int{2, 20, 40} {
			start := strings.Index(text, fmt.Sprintf("## Раздел %d\n", n))
			cm, err := c.AddComment(id, p, &core.Range{Start: start, End: start + 10}, fmt.Sprintf("Комментарий %d", n), nil, false)
			if err != nil {
				panic(err)
			}
			ids = append(ids, cm)
		}
		if err := c.Send(id, false); err != nil {
			panic(err)
		}
		c.Take(id)
		_, err = c.AgentFinish(id, nil, []core.Answer{{CommentID: ids[0], Text: "Готово"}, {CommentID: ids[1], Text: "Готово"}, {CommentID: ids[2], Text: "Уточните", Question: true}})
		if err != nil {
			panic(err)
		}
		if err := c.AcceptResponse(id); err != nil {
			panic(err)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "path": p})
	})
	http.Handle("/", server.New(c, "count-test"))
	panic(http.ListenAndServe("127.0.0.1:4885", nil))
}
