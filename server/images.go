package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Картинки в комментариях. Клиент присылает картинку из буфера или перетащенную в окно комментария;
// duet кладёт её в папку данных (`images/`) и отдаёт путь к файлу — его видит агент — и адрес для
// клиента. Одинаковые картинки — один файл.

var imageExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

var imageName = regexp.MustCompile(`^[0-9a-f]{20}\.(png|jpg|gif|webp)$`)

const maxImage = 20 << 20

func (s *Server) imagesDir() string { return filepath.Join(s.core.Dir(), "images") }

func (s *Server) imageRoutes() {
	s.mux.HandleFunc("POST /api/images", s.addImage)
	s.mux.HandleFunc("GET /api/images/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !imageName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=31536000, immutable")
		http.ServeFile(w, r, filepath.Join(s.imagesDir(), name))
	})
}

// addImage — тело {data: "data:image/png;base64,…"}; ответ {path, url}.
func (s *Server) addImage(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxImage*2)).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, "bad-request", err.Error(), nil)
		return
	}
	_, enc, ok := strings.Cut(b.Data, ";base64,")
	if !ok {
		enc = b.Data
	}
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(data) == 0 || len(data) > maxImage {
		writeErr(w, http.StatusBadRequest, "bad-image", "not an image", nil)
		return
	}
	ext, ok := imageExt[http.DetectContentType(data)]
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad-image", "not an image", nil)
		return
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:10]) + ext
	dir := s.imagesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "io", err.Error(), nil)
		return
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err != nil {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			writeErr(w, http.StatusInternalServerError, "io", err.Error(), nil)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			writeErr(w, http.StatusInternalServerError, "io", err.Error(), nil)
			return
		}
	}
	writeJSON(w, map[string]string{"path": path, "url": "/api/images/" + name})
}
