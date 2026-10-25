package main

import (
	"context"
	"os"

	"kuro/internal/cli"
)

func main() {
	err := cli.Execute(context.Background())
	os.Exit(cli.ExitCode(err))
}
