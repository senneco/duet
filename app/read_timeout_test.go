package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryReadTimeoutCLI(t *testing.T) {
	t.Setenv("DUET_HOME", t.TempDir())
	invoke := func(want int, args ...string) string {
		t.Helper()
		var out, stderr bytes.Buffer
		if code := Main(args, &out, &stderr); code != want {
			t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, &out, &stderr)
		}
		return out.String() + stderr.String()
	}
	if got := invoke(0, "config", "read-timeout"); got != "30s\n" {
		t.Fatal(got)
	}
	invoke(0, "config", "read-timeout", "12s")
	for _, bad := range []string{"90", "0s", "2h"} {
		invoke(1, "config", "read-timeout", bad)
	}
	if got := invoke(0, "config", "read-timeout"); got != "12s\n" {
		t.Fatal(got)
	}

	plugin := filepath.Join(t.TempDir(), "slow")
	script := `#!/bin/sh
case "$(cat)" in
  *'"operation":"hello"'*) printf '%s\n' '{"protocol":1,"result":{"operations":["detect","state","original"]}}' ;;
  *)
    if [ "${DUET_PLUGIN_TIMEOUT_MS:-0}" -gt 5000 ] && [ "$DUET_PLUGIN_TIMEOUT_MS" -le 12000 ]; then
        printf '%s\n' '{"protocol":1,"error":"operation timed out","error_code":"timeout"}'
    else
        printf '%s\n' '{"protocol":1,"error":"unexpected budget"}'
    fi ;;
esac
`
	if err := os.WriteFile(plugin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	invoke(0, "plugin", "add", "slow", plugin)
	if got := invoke(1, "plugin", "check", "slow", t.TempDir()); !strings.Contains(got, "duet config read-timeout 24s") {
		t.Fatal(got)
	}
}
