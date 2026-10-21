package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "attach":
			os.Exit(runAttach(os.Args[2:]))
		}
	}
	bind := flag.String("bind", defaultAddr(), "Address to bind (host:port; default $PTYD_ADDR or 127.0.0.1:4242). Use 0.0.0.0:4242 to expose on all interfaces.")
	token := flag.String("token", os.Getenv("PTYD_TOKEN"), "Auth token required as X-Ptyd-Token header (default $PTYD_TOKEN)")
	logDir := flag.String("log-dir", os.Getenv("PTYD_LOG_DIR"), "Optional directory to mirror raw PTY output to <id>.log files (default $PTYD_LOG_DIR)")
	persistDir := flag.String("persist-dir", os.Getenv("PTYD_PERSIST_DIR"), "Optional directory to persist per-terminal metadata sidecars so terminals are recoverable as 'ghosts' across daemon restarts (default $PTYD_PERSIST_DIR)")
	scrollback := flag.Int("scrollback-bytes", terminal.DefaultScrollbackBytes, "Per-terminal memory budget for retained output lines; worst-case memory is about terminals x this")
	flag.Parse()

	mgr := terminal.NewManagerWithOptions(terminal.Options{
		LogDir:          *logDir,
		PersistDir:      *persistDir,
		ScrollbackBytes: *scrollback,
	})
	srv := api.New(mgr, api.Config{Addr: *bind, Token: *token})

	// Bind the listener up front so a bind failure becomes a synchronous
	// startup error rather than racing with the "listening" log line.
	ln, err := srv.Listen()
	if err != nil {
		log.Fatalf("listen %s: %v", *bind, err)
	}
	logDirInfo := ""
	if *logDir != "" {
		logDirInfo = " log-dir=" + *logDir
	}
	persistInfo := ""
	if *persistDir != "" {
		persistInfo = " persist-dir=" + *persistDir
	}
	log.Printf("ptyd listening on %s (auth=%v)%s%s", ln.Addr(), *token != "", logDirInfo, persistInfo)
	if ip := ln.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() && *token == "" {
		log.Printf("WARNING: listening on a non-loopback address without a token; anyone who can reach it can run commands as this user")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		log.Printf("server error: %v", err)
	case sig := <-sigCh:
		log.Printf("received %v, shutting down", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// Terminate every child (SIGTERM, then SIGKILL after a grace period)
	// and wait for them to be reaped before exiting.
	mgr.CloseAll()
	log.Printf("shutdown complete")
}
