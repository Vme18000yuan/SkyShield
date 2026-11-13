package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yyhuni/lunafox/server/internal/localagent"
)

func main() {
	var config localagent.Config
	var check bool
	flag.StringVar(&config.CredentialsPath, "credentials", "", "absolute path to local Agent credentials")
	flag.StringVar(&config.ControlAddress, "server", "127.0.0.1:9090", "loopback Server gRPC address")
	flag.StringVar(&config.ExecutionRoot, "execution-root", "", "private directory for task workspaces")
	flag.StringVar(&config.EngineBinDir, "engine-bin-dir", "", "directory containing native Engine executables")
	flag.StringVar(&config.EngineSourceDir, "engine-source-dir", "", "directory containing Engine definitions")
	flag.StringVar(&config.PackageCacheRoot, "package-cache-root", "", "Server Engine package cache directory")
	flag.StringVar(&config.ProxySocket, "proxy-socket", "", "private Server proxy policy socket")
	flag.StringVar(&config.ToolsDir, "tools-dir", "", "directory containing local scanning tools")
	flag.StringVar(&config.Version, "version", "0.0.1-alpha.229", "Agent protocol version")
	flag.BoolVar(&check, "check", false, "check native Engine and tool dependencies")
	flag.Parse()
	if check {
		if err := localagent.CheckDependencies(config); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("local scanner dependencies are ready")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := localagent.Run(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
