// Отдельное тестовое ядро с вымышленными документами. Запуск: go run server/browsertest/position-fixture.go
package main

import (
	"encoding/json"
	"fmt"
	"github.com/senneco/duet/core"
	"github.com/senneco/duet/server"
	"net/http"
	"os"
	"strings"
)

func main() {
	dir, _ := os.MkdirTemp("", "duet-position-")
	c, e := core.Open(dir)
	if e != nil {
		panic(e)
	}
	s := server.New(c, "position-test")
	http.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		proj, _ := os.MkdirTemp(dir, "proj-")
		text := ""
		for i := 0; i < 100; i++ {
			paragraph := fmt.Sprintf("Текст раздела %d для проверки места чтения. ", i)
			if r.URL.Query().Get("long") == "1" {
				paragraph = strings.Repeat(paragraph, 12)
			}
			text += fmt.Sprintf("## Раздел %d\n\n%s\n\n", i, paragraph)
		}
		p := proj + fmt.Sprintf("/file-%d.md", len(text))
		os.WriteFile(p, []byte(text), 0600)
		q := proj + "/other.md"
		os.WriteFile(q, []byte(text), 0600)
		id, _, err := c.CreateReview("Проверка позиции", []core.Submission{{Path: p, Content: text, Original: &core.Original{Content: text}}, {Path: q, Content: text, Original: &core.Original{Content: text}}})
		if err != nil {
			panic(err)
		}
		c.AgentStart(id)
		next := "## Добавленный раздел\n\nНовый текст.\n\n" + text
		if r.URL.Query().Get("delete") == "1" {
			next = text[strings.Index(text, "## Раздел 20\n"):]
		}
		os.WriteFile(p, []byte(next), 0600)
		os.WriteFile(q, []byte(next), 0600)
		files := []core.Submission{{Path: p, Content: next}, {Path: q, Content: next}}
		if r.URL.Query().Get("untouched") == "1" {
			os.WriteFile(p, []byte(text), 0600)
			files = files[1:]
		}
		_, err = c.AgentFinish(id, files, nil)
		if err != nil {
			panic(err)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "path": p, "other": q})
	})
	http.Handle("/", s)
	panic(http.ListenAndServe("127.0.0.1:4882", nil))
}
