package main

import (
	"github.com/yyhuni/lunafox/server/internal/ailocal"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "ai-tool-worker" {
		args = args[1:]
	}
	os.Exit(ailocal.Main(args))
}
