package app

import (
	"github.com/senneco/duet/core"
	"github.com/senneco/duet/updates"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotRestoresFilesAndSymlinks(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "file")
	link := filepath.Join(d, "link")
	missing := filepath.Join(d, "missing")
	os.WriteFile(file, []byte("before"), 0600)
	os.Symlink("file", link)
	saved, e := snapshot([]string{file, link, missing})
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(file, []byte("after"), 0600)
	os.Remove(link)
	os.WriteFile(link, []byte("bad"), 0600)
	os.WriteFile(missing, []byte("new"), 0600)
	if e = restore(saved); e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(file)
	if string(data) != "before" {
		t.Fatal("file lost")
	}
	target, _ := os.Readlink(link)
	if target != "file" {
		t.Fatal("link lost")
	}
	if _, e = os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("new file remained")
	}
}
func TestApplyRollsBackFailedInstallationAndStartup(t *testing.T) {
	for _, phase := range []string{"install", "serve"} {
		t.Run(phase, func(t *testing.T) {
			e := setup(t)
			cfg := Config{Home: e.home, Port: e.port}
			d := filepath.Join(e.home, "bin")
			os.Mkdir(d, 0700)
			target := filepath.Join(d, "duet")
			if err := copyExe(bin, target); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.home, "claude"))
			t.Setenv("CODEX_HOME", filepath.Join(e.home, "codex"))
			stage := filepath.Join(e.home, "stage")
			os.Mkdir(stage, 0700)
			script := "#!/bin/sh\ncase \"$1\" in --update-protocol) echo 1;; install) cp \"$0\" \"$DUET_BIN/duet\"; mkdir -p \"$CODEX_HOME\"; echo changed > \"$CODEX_HOME/hooks.json\"; "
			if phase == "install" {
				script += "exit 7"
			} else {
				script += "exit 0"
			}
			script += ";; serve) exit 9;; esac\n"
			os.WriteFile(filepath.Join(stage, "duet"), []byte(script), 0700)
			c, err := core.Open(e.home)
			if err != nil {
				t.Fatal(err)
			}
			id, _, err := c.CreateReview("keep", []core.Submission{{Path: filepath.Join(e.home, "a"), Content: "accepted", Original: &core.Original{Content: "accepted", Source: "test"}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = c.AcceptFile(id, filepath.Join(e.home, "a")); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(target)
			if phase == "serve" {
				if err = cfg.startInstalled(target); err != nil {
					t.Fatal(err)
				}
			}
			source := updates.Config{Protocol: 1, Manifest: "https://example.invalid/release.json", Compiler: []string{"go"}, BinDir: d, Version: "old"}
			m := updates.Manifest{Protocol: 1, DataFormat: 1, Version: "new", Revision: strings.Repeat("a", 40)}
			if err = cfg.applyUpdate(stage, source, m); err == nil {
				t.Fatal("failure was ignored")
			}
			after, _ := os.ReadFile(target)
			if string(before) != string(after) {
				t.Fatal("binary not restored")
			}
			if _, err = os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")); !os.IsNotExist(err) {
				t.Fatal("hooks not restored")
			}
			if phase == "serve" {
				if _, ok := cfg.running(); !ok {
					t.Fatal("previous version not restarted")
				}
				if in, ok := cfg.running(); ok {
					cfg.stop(in)
				}
			}
			c, err = core.Open(e.home)
			if err != nil {
				t.Fatal(err)
			}
			v, err := c.File(id, filepath.Join(e.home, "a"))
			if err != nil || v.Base != "accepted" {
				t.Fatal("accepted base changed", err)
			}
		})
	}
}

func TestInterruptedUpdateRestoresCompleteInstallation(t *testing.T) {
	d := t.TempDir()
	cfg := Config{Home: filepath.Join(d, "home")}
	target := filepath.Join(d, "duet")
	plugin := filepath.Join(d, "plugin")
	config := filepath.Join(cfg.Home, updates.ConfigFile)
	os.MkdirAll(cfg.Home, 0700)
	os.WriteFile(target, []byte("old core"), 0700)
	os.WriteFile(plugin, []byte("old plugin"), 0700)
	os.WriteFile(config, []byte("old source"), 0600)
	saved, err := snapshot([]string{target, plugin, config})
	if err != nil {
		t.Fatal(err)
	}
	if err = updates.WriteJSON(cfg.journalPath(), installationJournal{Saved: saved, Target: target}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(target, []byte("new core"), 0700)
	os.WriteFile(plugin, []byte("new plugin"), 0700)
	if err = cfg.recoverUpdate(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{target: "old core", plugin: "old plugin", config: "old source"} {
		data, _ := os.ReadFile(p)
		if string(data) != want {
			t.Fatalf("%s: %s", p, data)
		}
	}
	if _, err = os.Stat(cfg.journalPath()); !os.IsNotExist(err) {
		t.Fatal("journal retained")
	}
}
