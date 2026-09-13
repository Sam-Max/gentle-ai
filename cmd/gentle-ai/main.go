package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/gentleman-programming/gentle-ai/v3/internal/app"
)

// version is set by GoReleaser via ldflags at build time.
var version = "dev"

func main() {
	app.Version = app.ResolveVersion(version)

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		// An error that classifies its own exit code (for example a refused
		// consent answer, which is a distinct outcome from a malformed
		// request) overrides the ordinary failure code.
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		os.Exit(1)
	}
}
