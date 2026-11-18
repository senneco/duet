// Пример транспорта: отдельный процесс поверх встроенного провайдера git.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/senneco/duet/vcs"
)

func main() {
	var q vcs.Request
	var result any
	err := json.NewDecoder(os.Stdin).Decode(&q)
	if err == nil && q.Protocol != vcs.ProtocolVersion {
		err = fmt.Errorf("unsupported protocol")
	}
	if err == nil {
		git := vcs.Git{}
		ctx := context.Background()
		switch q.Operation {
		case "hello":
			result = vcs.Capabilities{Operations: []string{"detect", "state", "original"}}
		case "detect":
			var w *vcs.Workspace
			w, err = git.Detect(ctx, q.Directory)
			if w != nil {
				w.Provider = "example-git"
			}
			result = w
		case "state", "original":
			if q.Workspace == nil {
				err = fmt.Errorf("missing workspace")
				break
			}
			if q.Operation == "state" {
				s, e := git.State(ctx, *q.Workspace)
				err = e
				if s.Changes == nil {
					s.Changes = []vcs.Change{}
				}
				result = s
			} else {
				result, err = git.Original(ctx, *q.Workspace, q.Revision, q.Path)
			}
		default:
			err = fmt.Errorf("unknown operation")
		}
	}
	reply := vcs.Reply{Protocol: vcs.ProtocolVersion}
	if err != nil {
		reply.Error = err.Error()
	} else {
		reply.Result, err = json.Marshal(result)
		if err != nil {
			reply.Error = err.Error()
		}
	}
	if json.NewEncoder(os.Stdout).Encode(reply) != nil {
		os.Exit(1)
	}
}
