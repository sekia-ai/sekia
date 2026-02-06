package main

import (
	"os"

	"github.com/sekia-ai/sekia/cmd/sekiactl/cmd"
)

func main() {
	rootCmd := cmd.NewRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
