package oast

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
)

// Start binds every configured listener before advertising a working runtime.
// The returned close function releases listeners and waits for retention work.
func (s *Service) Start(ctx context.Context, c *Collector) (func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	var closers []func()
	var wg sync.WaitGroup
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			cancel()
			for _, close := range closers {
				close()
			}
			wg.Wait()
		})
	}
	launch := func(run func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := run(); err != nil && ctx.Err() == nil {
				pkg.Error("OAST listener stopped", zap.Error(err))
			}
		}()
	}
	if s.config.HTTPListen != "" {
		listener, err := net.Listen("tcp", s.config.HTTPListen)
		if err != nil {
			closeAll()
			return nil, err
		}
		srv := &http.Server{Handler: c, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 * 1024}
		closers = append(closers, func() { _ = srv.Close(); _ = listener.Close() })
		launch(func() error {
			err := srv.Serve(listener)
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	}
	if s.config.DNSListen != "" {
		packet, err := net.ListenPacket("udp", s.config.DNSListen)
		if err != nil {
			closeAll()
			return nil, err
		}
		listener, err := net.Listen("tcp", s.config.DNSListen)
		if err != nil {
			_ = packet.Close()
			closeAll()
			return nil, err
		}
		handler := NewDNSCollector(c, s.config.DNSZone, s.config.DNSAnswerIP)
		udp := &dns.Server{PacketConn: packet, Handler: handler, UDPSize: 512, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second}
		tcp := &dns.Server{Listener: listener, Handler: handler, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxTCPQueries: 32}
		closers = append(closers, func() { _ = udp.Shutdown(); _ = packet.Close(); _ = tcp.Shutdown(); _ = listener.Close() })
		launch(udp.ActivateAndServe)
		launch(tcp.ActivateAndServe)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			cleanupCtx, stop := context.WithTimeout(ctx, 15*time.Second)
			if err := s.store.Cleanup(cleanupCtx); err != nil && ctx.Err() == nil {
				pkg.Error("OAST retention failed", zap.Error(err))
			}
			stop()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return closeAll, nil
}
