package main

import (
	"context"
	"os"

	"github.com/inercia/kubetnl/pkg/command"
)

func main() {
	ctx := context.Background()
	cmd := command.NewKubetnlCommand(os.Stdin, os.Stdout, os.Stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
