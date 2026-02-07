package main

import (
	"os"

	"github.com/sekia-ai/sekia/cmd/sekiactl/cmd"
)

var version = "dev"

func main() {
	cmd.Version = version
	rootCmd := cmd.NewRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
