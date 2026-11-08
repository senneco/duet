package server

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/senneco/duet/core"
)

type env struct {
	t    *testing.T
	c    *core.Core
	srv  *Server
	http *httptest.Server
	dir  string // папка проекта с файлами ревью
}

func setup(t *testing.T) *env {
	t.Helper()
	c, err := core.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := New(c, "b1")
	h := httptest.NewServer(s)
	t.Cleanup(func() { s.Close(); h.Close() })
	return &env{t: t, c: c, srv: s, http: h, dir: t.TempDir()}
}

// review — ревью из файлов на диске: имя → содержимое.
func (e *env) review(files map[string]string) string {
	e.t.Helper()
	var subs []core.Submission
	for name, content := range files {
		p := filepath.Join(e.dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
		subs = append(subs, core.Submission{Path: p, Content: content, Original: &core.Original{Content: content, Source: "из git, HEAD"}})
	}
	id, _, err := e.c.CreateReview("задача", subs)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) path(name string) string { return filepath.Join(e.dir, name) }

func (e *env) do(method, path string, body any, hdr map[string]string) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.http.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (e *env) post(path string, body any) (int, map[string]any) {
	e.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	code, data := e.do("POST", path, body, nil)
	var out map[string]any
	json.Unmarshal(data, &out)
	return code, out
}

type sse struct {
	name string
	data map[string]any
}

// events открывает поток событий и отдаёт их в канал.
func (e *env) events() <-chan sse {
	e.t.Helper()
	resp, err := http.Get(e.http.URL + "/api/events")
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan sse, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = line[7:]
			case strings.HasPrefix(line, "data: "):
				var d map[string]any
				json.Unmarshal([]byte(line[6:]), &d)
				ch <- sse{name, d}
			}
		}
		close(ch)
	}()
	return ch
}

// wait ждёт событие с этим именем, для которого ok вернул true.
func wait(t *testing.T, ch <-chan sse, name string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev, open := <-ch:
			if !open {
				t.Fatalf("поток событий закрылся, ждали %s", name)
			}
			if ev.name == name && (ok == nil || ok(ev.data)) {
				return ev.data
			}
		case <-timeout:
			t.Fatalf("не дождались события %s", name)
		}
	}
}

func hasReview(id string) func(map[string]any) bool {
	return func(d map[string]any) bool {
		for _, r := range d["reviews"].([]any) {
			if r == id {
				return true
			}
		}
		return false
	}
}

// Сценарий 96: всё, что меняется в ядре, приходит во все открытые вкладки само.
func TestChangesReachAllTabs(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "раз\n"})
	tab1, tab2 := e.events(), e.events()
	hello := wait(t, tab1, "hello", nil)
	if hello["build"] != "b1" {
		t.Fatalf("hello: %v", hello)
	}
	wait(t, tab2, "hello", nil)

	// Действие из одной вкладки видно в обеих.
	if code, out := e.post("/api/reviews/"+id+"/comment", map[string]any{"path": e.path("a.md"), "text": "почему?"}); code != 200 || out["id"] == "" {
		t.Fatalf("comment: %d %v", code, out)
	}
	wait(t, tab1, "change", hasReview(id))
	wait(t, tab2, "change", hasReview(id))

	// Изменение не из вкладки — например, агент отключился.
	if err := e.c.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	wait(t, tab1, "change", hasReview(id))
	_, data := e.do("GET", "/api/reviews/"+id, nil, nil)
	var rv core.ReviewView
	json.Unmarshal(data, &rv)
	if rv.Agent || rv.Unsent != 1 {
		t.Fatalf("ревью после изменений: %+v", rv)
	}

	// Изменение на диске тоже приходит само.
	e.post("/api/reviews/"+id+"/sync", nil)
	os.WriteFile(e.path("a.md"), []byte("два\n"), 0o644)
	e.c.Poll()
	wait(t, tab2, "change", hasReview(id))
	_, data = e.do("GET", "/api/reviews/"+id, nil, nil)
	json.Unmarshal(data, &rv)
	if rv.FromDisk == nil || len(rv.FromDisk.Files) != 1 {
		t.Fatalf("правка с диска не пришла: %+v", rv)
	}
}

// Сценарий 101: чужой сайт и чужое имя ничего не получают и ничего не меняют.
func TestOnlyThisMachine(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "раз\n"})
	comment := map[string]any{"path": e.path("a.md"), "text": "x"}

	// Сайт подменил адрес своего домена на эту машину.
	if code, _ := e.do("GET", "/api/reviews", nil, map[string]string{"Host": "evil.example:4848"}); code != http.StatusForbidden {
		t.Fatalf("чужое имя: %d", code)
	}
	// Чужой сайт шлёт действие из браузера.
	if code, _ := e.do("POST", "/api/reviews/"+id+"/comment", comment, map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("чужой сайт: %d", code)
	}
	// Простая форма с чужого сайта (не JSON).
	req, _ := http.NewRequest("POST", e.http.URL+"/api/reviews/"+id+"/comment", strings.NewReader("text=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("форма: %d", resp.StatusCode)
	}
	// Остановить duet чужой сайт тоже не может.
	if code, _ := e.do("POST", "/api/shutdown", map[string]any{}, map[string]string{"Origin": "http://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("shutdown: %d", code)
	}
	rv, _ := e.c.Review(id)
	if rv.Unsent != 0 {
		t.Fatal("чужой запрос оставил комментарий")
	}

	// Своя вкладка работает под любым именем этой машины.
	for _, host := range []string{"duet.localhost:4848", "localhost:4848", "127.0.0.1:4848", "[::1]:4848"} {
		if code, _ := e.do("POST", "/api/reviews/"+id+"/opened", map[string]any{"path": e.path("a.md")},
			map[string]string{"Host": host, "Origin": "http://" + host}); code != 200 {
			t.Fatalf("%s: %d", host, code)
		}
	}
	if code, _ := e.do("GET", "/r/"+id, nil, map[string]string{"Host": "duet.localhost:4848"}); code != 200 {
		t.Fatalf("страница: %d", code)
	}
}

// Сценарий 102: открыли ревью — сверка с диском с видимым прогрессом, дальше duet следит.
func TestSyncProgress(t *testing.T) {
	e := setup(t)
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d"} {
		files[n+".md"] = n + "\n"
	}
	id := e.review(files)
	// Ревью не принято целиком: есть неотправленный комментарий, значит duet сверяет диск.
	e.c.AddComment(id, e.path("a.md"), nil, "x", nil, false)
	tab := e.events()
	wait(t, tab, "hello", nil)
	os.WriteFile(e.path("b.md"), []byte("правка мимо duet\n"), 0o644)

	if code, _ := e.post("/api/reviews/"+id+"/sync", nil); code != 200 {
		t.Fatalf("sync: %d", code)
	}
	last := wait(t, tab, "sync", func(d map[string]any) bool { return d["review"] == id && d["done"] == d["total"] })
	if last["total"].(float64) != 4 {
		t.Fatalf("прогресс: %v", last)
	}
	_, data := e.do("GET", "/api/reviews/"+id, nil, nil)
	var rv core.ReviewView
	json.Unmarshal(data, &rv)
	if !rv.Synced || rv.FromDisk == nil || len(rv.FromDisk.Files) != 1 || rv.FromDisk.Files[0] != e.path("b.md") {
		t.Fatalf("после сверки: %+v", rv)
	}
}

// Список и дерево приходят без содержимого файлов, содержимое и отличия — отдельно.
func TestReadAPI(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "раз два\n"})
	if err := e.c.AgentStart(id); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e.path("a.md"), []byte("раз три\n"), 0o644) // агент пишет на диск сам
	if _, err := e.c.AgentFinish(id, []core.Submission{{Path: e.path("a.md"), Content: "раз три\n"}}, nil); err != nil {
		t.Fatal(err)
	}

	_, data := e.do("GET", "/api/reviews", nil, nil)
	var list []core.ReviewView
	json.Unmarshal(data, &list)
	if len(list) != 1 || !list[0].HasResponse || list[0].Files[0].Current != "" || list[0].Files[0].State != core.StateAccepted || list[0].Files[0].DiskChanged || !list[0].Files[0].Ripple {
		t.Fatalf("список: %s", data)
	}

	_, data = e.do("GET", "/api/reviews/"+id+"/file?path="+e.path("a.md"), nil, nil)
	var fv core.FileView
	json.Unmarshal(data, &fv)
	if fv.Current != "раз два\n" || fv.Response == nil || *fv.Response != "раз три\n" {
		t.Fatalf("файл: %s", data)
	}

	_, data = e.do("GET", "/api/reviews/"+id+"/diff?mode=response&path="+e.path("a.md"), nil, nil)
	var chunks []core.Chunk
	json.Unmarshal(data, &chunks)
	if len(chunks) != 1 || chunks[0].Kind != core.ChunkChanged {
		t.Fatalf("отличия: %s", data)
	}
	if code, _ := e.do("GET", "/api/reviews/"+id+"/diff?path="+e.path("a.md"), nil, nil); code != 200 {
		t.Fatalf("отличия от базы: %d", code)
	}
	if code, _ := e.do("GET", "/api/reviews/nope", nil, nil); code != 404 {
		t.Fatalf("нет ревью: %d", code)
	}
}

// Запреты ядра приходят клиенту кодом, по которому он показывает текст на своём языке.
func TestErrorCodes(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "раз\n"})
	a := e.path("a.md")

	if code, out := e.post("/api/reviews/"+id+"/send", nil); code != 409 || out["error"] != "nothing-to-send" {
		t.Fatalf("нечего отправлять: %d %v", code, out)
	}
	os.WriteFile(a, []byte("мимо duet\n"), 0o644)
	// Правка начата с «раз», а файл поменяли на диске — нужен ответ владельца.
	if code, out := e.post("/api/reviews/"+id+"/edit", map[string]any{"path": a, "content": "моё\n", "from": "раз\n"}); code != 409 || out["error"] != "changed-meanwhile" {
		t.Fatalf("поменяли, пока правили: %d %v", code, out)
	}
	if code, out := e.post("/api/reviews/"+id+"/edit", map[string]any{"path": a, "content": "моё\n", "from": "раз\n", "force": true}); code != 200 {
		t.Fatalf("сохранить всё равно: %d %v", code, out)
	}
	e.c.AgentStart(id)
	os.WriteFile(a, []byte("агент\n"), 0o644)
	e.c.AgentFinish(id, []core.Submission{{Path: a, Content: "агент\n"}}, nil)
	if code, out := e.post("/api/reviews/"+id+"/revert", map[string]any{"path": a}); code != 409 || out["error"] != "unviewed-response" {
		t.Fatalf("непросмотренный ответ: %d %v", code, out)
	}
	if code, _ := e.post("/api/reviews/"+id+"/accept-response", nil); code != 200 {
		t.Fatal("принять ответ")
	}
	if code, _ := e.post("/api/reviews/"+id+"/accept-file", map[string]any{"path": a}); code != 200 {
		t.Fatal("принять файл")
	}
	f, _ := e.c.File(id, a)
	if f.State != core.StateAccepted {
		t.Fatalf("файл: %v", f.State)
	}
	e.c.Disconnect(id)
	e.post("/api/reviews/"+id+"/comment", map[string]any{"path": a, "text": "x"})
	if code, out := e.post("/api/reviews/"+id+"/send", nil); code != 409 || out["error"] != "no-agent" {
		t.Fatalf("нет агента: %d %v", code, out)
	}
	if code, out := e.post("/api/reviews/"+id+"/nope", nil); code != 404 {
		t.Fatalf("неизвестное действие: %d %v", code, out)
	}
}

// По запросу остановки сервер сначала отвечает, потом останавливается.
func TestShutdown(t *testing.T) {
	e := setup(t)
	stopped := make(chan bool, 1)
	e.srv.OnShutdown = func() { stopped <- true }
	tab := e.events()
	wait(t, tab, "hello", nil)
	if code, _ := e.post("/api/shutdown", nil); code != 200 {
		t.Fatal(code)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("не остановился")
	}
	e.srv.Close()
	select {
	case _, open := <-tab:
		for open {
			_, open = <-tab
		}
	case <-time.After(time.Second):
		t.Fatal("поток событий не закрылся")
	}
}

// Клиент: страница по адресам списка и ревью, модули и стили — с правильным типом, чтобы браузер их взял.
func TestClientFiles(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"/", "/r/r1"} {
		code, body := e.do("GET", p, nil, nil)
		if code != 200 || !strings.Contains(string(body), `src="/web/js/main.js"`) {
			t.Fatalf("%s: %d", p, code)
		}
	}
	for p, typ := range map[string]string{"/web/js/main.js": "javascript", "/web/js/view.js": "javascript", "/web/app.css": "text/css"} {
		resp, err := http.Get(e.http.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), typ) {
			t.Fatalf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

// Шаг 6b: комментарий к удалённому тексту и картинки в комментариях.
func TestRemovedCommentAndImages(t *testing.T) {
	e := setup(t)
	id := e.review(map[string]string{"a.md": "один\nдва\n"})
	a := e.path("a.md")
	code, out := e.post("/api/reviews/"+id+"/comment", map[string]any{"path": a, "text": "зачем?", "range": map[string]int{"start": 5, "end": 5}, "was": "полтора"})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	fv, _ := e.c.File(id, a)
	if len(fv.Comments) != 1 || fv.Comments[0].Was != "полтора" || !fv.Comments[0].Removed {
		t.Fatalf("%+v", fv.Comments)
	}

	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	code, out = e.post("/api/images", map[string]any{"data": "data:image/png;base64," + png})
	if code != 200 || !strings.HasSuffix(out["path"].(string), ".png") || !strings.HasPrefix(out["url"].(string), "/api/images/") {
		t.Fatalf("%d %v", code, out)
	}
	if _, err := os.Stat(out["path"].(string)); err != nil {
		t.Fatal(err)
	}
	if code, data := e.do("GET", out["url"].(string), nil, nil); code != 200 || len(data) == 0 {
		t.Fatalf("get image: %d", code)
	}
	if code, _ := e.post("/api/images", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("<script>"))}); code != 400 {
		t.Fatalf("not an image: %d", code)
	}
	if code, _ := e.do("GET", "/api/images/..%2fstate.json", nil, nil); code != 404 {
		t.Fatalf("bad name: %d", code)
	}
}
