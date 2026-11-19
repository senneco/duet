package updates

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func archive(t *testing.T, path string, headers []*tar.Header) {
	t.Helper()
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	for _, h := range headers {
		if e = w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			if _, e = w.Write(make([]byte, h.Size)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	gz.Close()
	f.Close()
}
func TestSourceArchiveRejectsEscapesAndLinks(t *testing.T) {
	for _, h := range []*tar.Header{{Name: "repo/../outside", Typeflag: tar.TypeReg}, {Name: "/absolute", Typeflag: tar.TypeReg}, {Name: "repo/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}, {Name: "repo/device", Typeflag: tar.TypeFifo}} {
		t.Run(h.Name, func(t *testing.T) {
			dir := t.TempDir()
			a := filepath.Join(dir, "a.tgz")
			archive(t, a, []*tar.Header{h})
			if extract(a, filepath.Join(dir, "out")) == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestSourceArchiveWithGlobalPAXHeader(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "source.tar.gz")
	archive(t, input, []*tar.Header{
		{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": strings.Repeat("a", 40)}},
		{Name: "repo/", Typeflag: tar.TypeDir},
		{Name: "repo/go.mod", Typeflag: tar.TypeReg},
	})
	out := filepath.Join(dir, "out")
	if err := extract(input, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "pax_global_header")); !os.IsNotExist(err) {
		t.Fatalf("metadata written as a file: %v", err)
	}
}

func TestDuplicateSourceFileRejected(t *testing.T) {
	d := t.TempDir()
	a := filepath.Join(d, "a.tgz")
	h := &tar.Header{Name: "repo/go.mod", Typeflag: tar.TypeReg}
	archive(t, a, []*tar.Header{h, h})
	if extract(a, filepath.Join(d, "out")) == nil {
		t.Fatal("duplicate accepted")
	}
}
func validManifest() Manifest {
	return Manifest{Protocol: 1, DataFormat: 1, Version: "r-one", Revision: strings.Repeat("a", 40)}
}
func TestManifestRejectsMixedOrIncompleteRelease(t *testing.T) {
	for _, edit := range []func(*Manifest){func(m *Manifest) { m.Revision = "main" }, func(m *Manifest) { m.DataFormat = 2 }, func(m *Manifest) { m.Files = []File{{Name: "../bad"}} }, func(m *Manifest) { m.Plugins = map[string]string{"x": "missing"} }, func(m *Manifest) { m.Plugins = map[string]string{"git": "duet"} }} {
		m := validManifest()
		edit(&m)
		if m.Validate() == nil {
			t.Fatal("invalid release accepted", m)
		}
	}
}
func TestPrepareFailureKeepsInstallation(t *testing.T) {
	for _, failure := range []string{"none", "build", "checksum", "download"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			m := validManifest()
			plugin := []byte("#!/bin/sh\nexit 0\n")
			sum := sha256.Sum256(plugin)
			m.Files = []File{{Name: "provider", Source: "plugin", SHA256: hex.EncodeToString(sum[:])}}
			m.Plugins = map[string]string{"example": "provider"}
			if failure == "checksum" {
				m.Files[0].SHA256 = strings.Repeat("0", 64)
			}
			manifest, _ := json.Marshal(m)
			os.WriteFile(filepath.Join(dir, "manifest"), manifest, 0600)
			os.WriteFile(filepath.Join(dir, "plugin"), plugin, 0600)
			archive(t, filepath.Join(dir, "source"), []*tar.Header{{Name: "repo/go.mod", Typeflag: tar.TypeReg}})
			helper := filepath.Join(dir, "fetch")
			os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$FAILURE\" != download ] || exit 1\ncase \"$2\" in manifest) f=manifest;; plugin) f=plugin;; *) f=source;; esac\ncp \"$FIXTURE/$f\" \"$3\"\n"), 0700)
			compiler := filepath.Join(dir, "compiler")
			os.WriteFile(compiler, []byte("#!/bin/sh\n[ \"$GOTOOLCHAIN\" = local ] && [ \"$GOWORK\" = off ] && [ \"$GOENV\" = off ] && [ -z \"$GOFLAGS\" ] || exit 3\nif [ \"$1\" = version ]; then echo 'go version go1.25.0 darwin/arm64'; exit; fi\n[ \"$FAILURE\" != build ] || exit 2\nprintf '#!/bin/sh\\nexit 0\\n' > \"$5\"\nchmod +x \"$5\"\n"), 0700)
			t.Setenv("FIXTURE", dir)
			t.Setenv("FAILURE", failure)
			t.Setenv("GOFLAGS", "-bad-flag")
			t.Setenv("GOTOOLCHAIN", "auto")
			home := filepath.Join(dir, "home")
			os.Mkdir(home, 0700)
			old := filepath.Join(home, "duet")
			os.WriteFile(old, []byte("old"), 0700)
			b := Builder{Config: Config{Protocol: 1, Manifest: "manifest", Fetch: []string{helper}, Compiler: []string{compiler}, BinDir: home}}
			stage, err := b.Prepare(context.Background(), home, m)
			if failure == "none" {
				if err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(filepath.Join(stage, "provider"))
				if string(data) != string(plugin) {
					t.Fatal("wrong file")
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				files, _ := os.ReadDir(filepath.Join(home, "updates"))
				if len(files) != 0 {
					t.Fatal("partial release retained")
				}
			}
			data, _ := os.ReadFile(old)
			if string(data) != "old" {
				t.Fatal("installation changed while preparing")
			}
		})
	}
}
func TestReadReleaseAndTooOldCompiler(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "go")
	os.WriteFile(goFile, []byte("#!/bin/sh\necho 'go version go1.24.9 linux/amd64'\n"), 0700)
	if e := Compile(context.Background(), []string{goFile}, dir, filepath.Join(dir, "duet")); e == nil || !strings.Contains(e.Error(), "1.25") {
		t.Fatal(e)
	}
}

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPrepareBinaryRelease(t *testing.T) {
	const payload = "fixture binary"
	target := runtime.GOOS + "-" + runtime.GOARCH
	sum := sha256.Sum256([]byte(payload))
	m := validManifest()
	m.Version = "v0.1.0"
	m.Binaries = map[string]string{target: hex.EncodeToString(sum[:])}
	home := t.TempDir()
	b := Builder{
		Config: Config{Protocol: 1, Manifest: ManifestURL, BinDir: home},
		Client: &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
			want := "https://github.com/senneco/duet/releases/download/v0.1.0/duet-" + target
			if req.URL.String() != want {
				t.Fatalf("got %s, want %s", req.URL, want)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
		})},
	}
	stage, err := b.Prepare(context.Background(), home, m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "duet"))
	if err != nil || string(got) != payload {
		t.Fatalf("staged binary: %q (%v)", got, err)
	}
	m.Binaries[target] = strings.Repeat("0", 64)
	if _, err := b.Prepare(context.Background(), home, m); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("bad checksum: %v", err)
	}
}
