// Command inspector-gopher is the Go profiler MCP server: it collects
// pprof/trace artifacts locally and from Docker containers and analyzes
// bottlenecks for AI agents and developers.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/h0rn3t/inspector-gopher/internal/collector"
	"github.com/h0rn3t/inspector-gopher/internal/config"
	"github.com/h0rn3t/inspector-gopher/internal/debug"
	dockerpkg "github.com/h0rn3t/inspector-gopher/internal/docker"
	"github.com/h0rn3t/inspector-gopher/internal/docker/dockersdk"
	"github.com/h0rn3t/inspector-gopher/internal/mcp"
	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
	"github.com/h0rn3t/inspector-gopher/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "inspector-gopher:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", "", "path to targets.yaml (YAML or JSON)")
		httpAddr   = flag.String("http", "", "serve Streamable HTTP on addr (e.g. 127.0.0.1:8080)")
		stdio      = flag.Bool("stdio", true, "serve MCP on stdin/stdout")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("inspector-gopher", version.Version)
		return nil
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *httpAddr != "" {
		cfg.Server.HTTPAddr = *httpAddr
	}
	// --http "" explicitly set with = clears; default keeps config value.
	if cfg.Server.HTTPAddr == "" && !*stdio {
		return fmt.Errorf("no transport selected: pass --stdio or --http addr")
	}

	st := store.New(20, store.DefaultCacheDir())
	if n := st.Restore(); n > 0 {
		logger.Info("restored profiles from cache", "count", n)
	}
	reg := targets.New(cfg)
	coll := collector.New(st, cfg.MaxProfileBytes(), time.Duration(cfg.Limits.DefaultTimeoutS)*time.Second)

	var lister *dockerpkg.Lister
	var rawClient dockerpkg.Client
	if len(cfg.Docker.Allowlist) > 0 || cfg.Docker.Socket != "" {
		raw, err := dockersdk.New(cfg.Docker.Socket)
		if err != nil {
			logger.Warn("docker unavailable", "err", err)
		} else {
			rawClient = raw
			lister = dockerpkg.NewLister(raw, cfg.Docker.Allowlist, cfg.Docker.DefaultPprofPort, logger).
				WithDAPPort(cfg.Docker.DefaultDAPPort, func(ctx context.Context, addr string) bool {
					return debug.Probe(ctx, addr, 3*time.Second)
				})
		}
	}

	dbgTimeout := time.Duration(cfg.DebugCfg.TimeoutS) * time.Second
	dbg := debug.NewManager(cfg.DebugCfg.MaxSessions, dbgTimeout)

	srv := mcp.New(mcp.Deps{
		Config: cfg, Registry: reg, Collector: coll,
		Store: st, Docker: lister, DockerRaw: rawClient, Debugger: dbg,
		Logger: logger, StartTime: time.Now(),
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// When both transports run, stdio EOF (no piped client) must not kill
	// the HTTP server: only propagate stdio errors when it is the sole
	// transport.
	httpOn := cfg.Server.HTTPAddr != ""
	stdioOn := *stdio && cfg.Server.Stdio
	errCh := make(chan error, 2)
	if stdioOn && !httpOn {
		go func() { errCh <- srv.RunStdio(ctx) }()
	} else if stdioOn {
		go func() {
			if err := srv.RunStdio(ctx); err != nil {
				logger.Warn("stdio ended", "err", err)
			}
		}()
	} else if *stdio && !cfg.Server.Stdio {
		logger.Info("stdio disabled by config")
	}
	if httpOn {
		logger.Info("serving HTTP", "addr", cfg.Server.HTTPAddr)
		go func() { errCh <- srv.RunHTTP(ctx, cfg.Server.HTTPAddr) }()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}
