// Package main contains the cage command-line entry point.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/2h2d-co/cage/internal/cage"
)

var version = "dev"

func main() {
	cmd := cage.NewRootCommand(version)
	if err := cmd.Execute(); err != nil {
		var exitErr cage.ExitError
		code := 1
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		fmt.Fprintln(os.Stderr, "Error:", cage.Redact(err.Error()))
		os.Exit(code)
	}
}
