package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yyhuni/lunafox/server/internal/deploymentnetwork"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var configPath, healthSocket string
	flag.StringVar(&configPath, "config", "", "private host network configuration file")
	flag.StringVar(&healthSocket, "health-socket", "", "check the helper's private Docker socket")
	flag.Parse()
	if healthSocket != "" {
		client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", healthSocket)
		}}}
		response, err := client.Get("http://helper/_skyshield/health")
		if err != nil {
			return fmt.Errorf("network helper is not ready")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("network helper is not ready")
		}
		return nil
	}
	cfg, err := deploymentnetwork.LoadConfig(configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	helper, err := deploymentnetwork.Start(ctx, cfg)
	if err != nil {
		return err
	}
	defer helper.Close()
	fmt.Fprintln(os.Stdout, "SkyShield host network adapter is ready")
	<-ctx.Done()
	return nil
}
