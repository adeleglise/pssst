package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/cache"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/collector"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/config"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/scheduler"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/server"
	"github.com/prometheus/client_golang/prometheus"
)

var version = "dev"

func main() { os.Exit(run()) }
func run() int {
	configPath := flag.String("config", "pssst.yml", "Path to YAML configuration (secrets belong in the file or environment)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return 0
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if flag.NArg() != 0 {
		log.Error("unexpected positional arguments")
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("invalid configuration", "reason", err.Error())
		return 1
	}
	listener, err := net.Listen("tcp", cfg.Server.ListenAddress)
	if err != nil {
		log.Error("cannot listen on configured address")
		return 1
	}
	c := cache.New(cfg)
	reg := prometheus.NewRegistry()
	reg.MustRegister(collector.New(c, version))
	srv := server.New(cfg.Server.ListenAddress, server.Handler(reg, c))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pollDone := make(chan struct{})
	go func() { defer close(pollDone); scheduler.New(cfg, c, log).Run(ctx) }()
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(listener) }()
	log.Info("pssst started", "version", version, "psps", len(cfg.PSPs))
	exitCode := 0
	select {
	case <-ctx.Done():
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP server stopped unexpectedly")
			exitCode = 1
		}
	}
	c.Stop()
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP shutdown deadline exceeded")
		_ = srv.Close()
		exitCode = 1
	}
	<-pollDone
	log.Info("pssst stopped")
	return exitCode
}
