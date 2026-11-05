// Команда duet.
package main

import (
	"os"

	"github.com/senneco/duet/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdout, os.Stderr))
}
