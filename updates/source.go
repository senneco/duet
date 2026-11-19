// Package updates получает закреплённые исходники и собирает отдельный выпуск.
// Пакет не меняет работающую установку и пользовательские данные.
package updates

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const ManifestURL = "https://github.com/senneco/duet/releases/latest/download/release.json"
const ConfigFile = "update-source.json"

// Config задаётся установщиком. Fetch — доверенная локальная программа,
// получающая аргументы read, ссылку и путь результата. Shell не используется.
type Config struct {
	Protocol int      `json:"protocol"`
	Manifest string   `json:"manifest"`
	Fetch    []string `json:"fetch,omitempty"`
	Compiler []string `json:"compiler"`
	BinDir   string   `json:"binDir"`
	Version  string   `json:"version"`
}

type File struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

// Manifest публикуется после проверки исходников и всех дополнительных файлов.
// Revision — полный коммит публичного репозитория. DataFormat ограничивает откат
// выпусками с совместимым форматом пользовательских данных.
type Manifest struct {
	Protocol   int               `json:"protocol"`
	Version    string            `json:"version"`
	Revision   string            `json:"revision"`
	DataFormat int               `json:"dataFormat"`
	Binaries   map[string]string `json:"binaries,omitempty"`
	Files      []File            `json:"files,omitempty"`
	Plugins    map[string]string `json:"plugins,omitempty"`
}

var versionRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
var revisionRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (m Manifest) Validate() error {
	if m.Protocol != 1 || m.DataFormat != 1 || !nameRE.MatchString(m.Version) || !revisionRE.MatchString(m.Revision) {
		return errors.New("unsupported or invalid release manifest")
	}
	for target, hash := range m.Binaries {
		switch target {
		case "darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64":
		default:
			return errors.New("unsupported release platform")
		}
		if !hashRE.MatchString(hash) {
			return errors.New("invalid binary checksum")
		}
	}
	seen := map[string]bool{"duet": true, ConfigFile: true, "release.json": true, "source": true, "source.tar.gz": true}
	for _, f := range m.Files {
		if !nameRE.MatchString(f.Name) || seen[f.Name] || f.Source == "" || !hashRE.MatchString(f.SHA256) {
			return errors.New("invalid or duplicate release file")
		}
		seen[f.Name] = true
	}
	for name, file := range m.Plugins {
		if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`).MatchString(name) || name == "git" || file == "duet" || !seen[file] || file == ConfigFile || file == "release.json" || file == "source" || file == "source.tar.gz" {
			return errors.New("invalid release plugin")
		}
	}
	return nil
}

func ReadConfig(home string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(filepath.Join(home, ConfigFile))
	if err != nil {
		return cfg, err
	}
	if err = json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Protocol != 1 || c.Manifest == "" || (len(c.Compiler) == 0 && (c.Manifest != ManifestURL || len(c.Fetch) != 0)) || !filepath.IsAbs(c.BinDir) {
		return errors.New("invalid update source configuration")
	}
	return nil
}

func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

type Builder struct {
	Config   Config
	Client   *http.Client
	Progress func(string)
}

func (b Builder) progress(s string) {
	if b.Progress != nil {
		b.Progress(s)
	}
}

func (b Builder) fetch(ctx context.Context, ref, target string, limit int64) error {
	if len(b.Config.Fetch) > 0 {
		args := append(append([]string{}, b.Config.Fetch[1:]...), "read", ref, target)
		cmd := exec.CommandContext(ctx, b.Config.Fetch[0], args...)
		// Не включаем stderr помощника в диагностику: там могут оказаться секреты.
		if err := cmd.Run(); err != nil {
			return errors.New("update source is unavailable; check access and try again")
		}
		st, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || st.Size() > limit {
			return errors.New("invalid source response")
		}
		return nil
	}
	if !strings.HasPrefix(ref, "https://") {
		return errors.New("update source must use HTTPS")
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", ref, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("cannot reach update source")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("update source returned HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("update download is too large")
	}
	return closeErr
}

func (b Builder) Check(ctx context.Context) (Manifest, error) {
	var m Manifest
	if err := b.Config.Validate(); err != nil {
		return m, err
	}
	dir, err := os.MkdirTemp("", "duet-check-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "manifest")
	if err = b.fetch(ctx, b.Config.Manifest, path, 1<<20); err != nil {
		return m, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, errors.New("invalid release manifest")
	}
	return m, m.Validate()
}

// Prepare возвращает готовый каталог. При любой ошибке частичный выпуск удаляется.
func (b Builder) Prepare(ctx context.Context, home string, m Manifest) (dir string, err error) {
	if err = m.Validate(); err != nil {
		return "", err
	}
	parent := filepath.Join(home, "updates")
	if err = os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	dir, err = os.MkdirTemp(parent, "release-")
	if err != nil {
		return "", err
	}
	defer func(staged string) {
		if err != nil {
			os.RemoveAll(staged)
		}
	}(dir)
	b.progress("downloading")
	archive := filepath.Join(dir, "source.tar.gz")
	if len(b.Config.Compiler) == 0 {
		target := runtime.GOOS + "-" + runtime.GOARCH
		hash := m.Binaries[target]
		if hash == "" || !versionRE.MatchString(m.Version) || len(m.Files) != 0 || len(m.Plugins) != 0 {
			return "", errors.New("no compatible binary release")
		}
		path := filepath.Join(dir, "duet")
		url := "https://github.com/senneco/duet/releases/download/" + m.Version + "/duet-" + target
		if err = b.fetch(ctx, url, path, 64<<20); err != nil {
			return "", err
		}
		if err = verifyExecutable(path, hash); err != nil {
			return "", err
		}
	} else {
		if err = b.fetch(ctx, "https://github.com/senneco/duet/archive/"+m.Revision+".tar.gz", archive, 64<<20); err != nil {
			return "", err
		}
		source := filepath.Join(dir, "source")
		if err = extract(archive, source); err != nil {
			return "", err
		}
		b.progress("building")
		if err = Compile(ctx, b.Config.Compiler, source, filepath.Join(dir, "duet")); err != nil {
			return "", err
		}
		os.Remove(archive)
		os.RemoveAll(source)
	}
	for _, f := range m.Files {
		path := filepath.Join(dir, f.Name)
		if err = b.fetch(ctx, f.Source, path, 32<<20); err != nil {
			return "", err
		}
		if err = verifyExecutable(path, f.SHA256); err != nil {
			return "", err
		}
	}
	if err = WriteJSON(filepath.Join(dir, "release.json"), m); err != nil {
		return "", err
	}
	return dir, nil
}

func verifyExecutable(path, hash string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return fmt.Errorf("checksum mismatch: %s", filepath.Base(path))
	}
	return os.Chmod(path, 0755)
}

// Compile не наследует настройки, меняющие смысл сборки, и не загружает Go
// автоматически. Кеш компиляции стандартный: повторная сборка использует его.
func Compile(ctx context.Context, compiler []string, source, target string) error {
	if len(compiler) == 0 {
		return errors.New("Go 1.25 or newer is required")
	}
	env := os.Environ()
	blocked := []string{"GOTOOLCHAIN=", "GOENV=", "GOFLAGS=", "GOWORK=", "CGO_ENABLED=", "GOOS=", "GOARCH=", "GOROOT="}
	filtered := make([]string, 0, len(env)+6)
	for _, e := range env {
		skip := false
		for _, key := range blocked {
			if strings.HasPrefix(e, key) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered, "GOTOOLCHAIN=local", "GOENV=off", "GOWORK=off", "CGO_ENABLED=0", "GOFLAGS=")
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, compiler[0], append(append([]string{}, compiler[1:]...), args...)...)
		cmd.Dir, cmd.Env = source, filtered
		return cmd.CombinedOutput()
	}
	version, err := run("version")
	if err != nil {
		return errors.New("cannot run the configured Go compiler")
	}
	var major, minor int
	if _, err = fmt.Sscanf(string(version), "go version go%d.%d", &major, &minor); err != nil || major < 1 || major == 1 && minor < 25 {
		return errors.New("Go 1.25 or newer is required; update your compiler and try again")
	}
	if _, err = run("build", "-trimpath", "-buildvcs=false", "-o", target, "./cmd/duet"); err != nil {
		return errors.New("source build failed; the installed duet has not changed")
	}
	return nil
}

func extract(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	var total int64
	var root string
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		name := strings.TrimSuffix(h.Name, "/")
		parts := strings.SplitN(name, "/", 2)
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root || root == "" || root == "." || root == ".." {
			return errors.New("invalid archive root")
		}
		if len(parts) == 1 {
			if h.Typeflag == tar.TypeDir {
				continue
			}
			return errors.New("invalid source archive")
		}
		rel := parts[1]
		if !filepath.IsLocal(rel) || strings.Contains(rel, "\\") || filepath.ToSlash(filepath.Clean(rel)) != rel || seen[rel] {
			return errors.New("invalid or duplicate source path")
		}
		seen[rel] = true
		path := filepath.Join(dest, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if h.Size < 0 || total > 128<<20 {
				return errors.New("source archive is too large")
			}
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return err
			}
			out, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			_, e = io.Copy(out, tr)
			closeErr := out.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("links and special files are not allowed in source archives")
		}
	}
	if _, err = os.Stat(filepath.Join(dest, "go.mod")); err != nil {
		return errors.New("source archive has no go.mod")
	}
	return nil
}
