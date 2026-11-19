package app

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/senneco/duet/vcs"
)

func (cfg Config) config(args []string, out io.Writer) error {
	if len(args) < 1 || len(args) > 2 || args[0] != "read-timeout" {
		return fmt.Errorf("usage: duet config read-timeout [DURATION]")
	}
	if len(args) == 1 {
		timeout, err := vcs.ReadTimeout(cfg.Home)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, timeout)
		return nil
	}
	timeout, err := vcs.ParseReadTimeout(args[1])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Home, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(cfg.Home, ".read-timeout-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = fmt.Fprintln(f, timeout)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), vcs.ReadTimeoutPath(cfg.Home)); err != nil {
		return err
	}
	fmt.Fprintln(out, timeout)
	return nil
}

func (cfg Config) reviewClient() *http.Client {
	client := *agentClient
	if timeout, err := vcs.ReadTimeout(cfg.Home); err == nil {
		client.Timeout = max(client.Timeout, timeout+15*time.Second)
	}
	return &client
}
