package main

import (
	"context"
	"fmt"
	"os"

	engine "github.com/yyhuni/lunafox/engine-go/protocol"
	"github.com/yyhuni/lunafox/engine-go/sdk"
)

func main() {
	if os.Getenv("DB_PASSWORD") != "" {
		fmt.Fprintln(os.Stderr, "Server credential leaked to Engine")
		os.Exit(1)
	}
	if value := os.Getenv("HTTP_PROXY"); value != "" && value != "http://127.0.0.1:1234" {
		fmt.Fprintln(os.Stderr, "configured proxy was not passed to Engine")
		os.Exit(1)
	}
	err := sdk.Run(func(ctx context.Context, snapshot *engine.EngineExecutionContext, reporter sdk.Reporter, inputs interface {
		SubdomainsPath(context.Context) (string, error)
		HostPortsPath(context.Context) (string, error)
		WebsiteURLsPath(context.Context) (string, error)
		EndpointURLsPath(context.Context) (string, error)
	}) error {
		if snapshot.GetTarget().GetValue() != "example.com" {
			return fmt.Errorf("unexpected target")
		}
		path, err := inputs.SubdomainsPath(ctx)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil || (string(content) != "a.example.com\n" && len(content) != 0) {
			return fmt.Errorf("unexpected input")
		}
		if err := reporter.ReportProgress(ctx, "fixture progress"); err != nil {
			return err
		}
		diagnostics, err := reporter.GeneratedDiagnostics()
		if err != nil {
			return err
		}
		if err := diagnostics.RegisterResultType("asset.subdomain.v1"); err != nil {
			return err
		}
		if err := diagnostics.ObserveResultReceived("asset.subdomain.v1"); err != nil {
			return err
		}
		if err := diagnostics.ObserveResultEncoded("asset.subdomain.v1"); err != nil {
			return err
		}
		sink, err := reporter.ResultSink("asset.subdomain.v1")
		if err != nil {
			return err
		}
		items := make(chan []byte, 1)
		items <- []byte(`{"dnsName":"a.example.com"}`)
		close(items)
		return sink.Submit(ctx, items)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
