package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func main() {
	if len(os.Args) > 1 {
		if run, ok := commands[os.Args[1]]; ok {
			os.Exit(run(os.Args[2:]))
		}
	}
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage+"\nDaemon flags:\n")
		flag.PrintDefaults()
	}
	bind := flag.String("bind", defaultAddr(), "Address to bind (host:port; default $PTYD_ADDR or 127.0.0.1:4242). Use 0.0.0.0:4242 to expose on all interfaces.")
	token := flag.String("token", os.Getenv("PTYD_TOKEN"), "Auth token required as X-Ptyd-Token header (default $PTYD_TOKEN, else a random one); saved for the CLI in ~/.local/state/ptyd/token-<port>")
	logDir := flag.String("log-dir", os.Getenv("PTYD_LOG_DIR"), "Optional directory to mirror raw PTY output to <id>.log files (default $PTYD_LOG_DIR)")
	persistDir := flag.String("persist-dir", os.Getenv("PTYD_PERSIST_DIR"), "Directory where terminals are saved so they survive a daemon restart (default $PTYD_PERSIST_DIR, else ~/.local/state/ptyd/terminals-<port>; --persist-dir= turns saving off)")
	scrollback := flag.Int("scrollback-bytes", terminal.DefaultScrollbackBytes, "Per-terminal memory budget for retained output lines; worst-case memory is about terminals x this")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "ptyd: unknown command %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
	if *token == "" {
		*token = client.NewToken()
	}
	if _, set := os.LookupEnv("PTYD_PERSIST_DIR"); !set && !flagSet("persist-dir") {
		if dir, err := client.PersistDir(*bind); err == nil {
			*persistDir = dir
		}
	}
	if term.IsTerminal(int(os.Stderr.Fd())) {
		log.SetFlags(0) // started by hand: no timestamps among the user's prompts
	}

	mgr := terminal.NewManagerWithOptions(terminal.Options{
		LogDir:          *logDir,
		PersistDir:      *persistDir,
		ScrollbackBytes: *scrollback,
	})
	srv := api.New(mgr, api.Config{Addr: *bind, Token: *token})

	// Bind the listener up front so a bind failure becomes a synchronous
	// startup error rather than racing with the "listening" log line.
	ln, err := srv.Listen()
	if errors.Is(err, syscall.EADDRINUSE) {
		log.Fatalf("listen %s: address in use (is ptyd already running? try `ptyd ls`)", *bind)
	}
	if err != nil {
		log.Fatalf("listen %s: %v", *bind, err)
	}
	// Save the token where the CLI looks for it. The daemon is the only
	// one on this port, so a leftover file from a crash is replaced.
	tokenFile, err := client.WriteToken(ln.Addr().String(), *token)
	if err != nil {
		log.Printf("could not save the token for the CLI: %v", err)
	} else {
		defer os.Remove(tokenFile)
	}
	logDirInfo := ""
	if *logDir != "" {
		logDirInfo = " log-dir=" + *logDir
	}
	persistInfo := ""
	if *persistDir != "" {
		persistInfo = " persist-dir=" + *persistDir
	}
	log.Printf("ptyd listening on %s%s%s", ln.Addr(), logDirInfo, persistInfo)
	// Bring back terminals whose restore policy asks for it.
	mgr.RestoreGhosts()

	// Started with `ptyd &`, the daemon outlives the shell and the
	// window it was started from (zsh sends background jobs SIGHUP).
	signal.Ignore(syscall.SIGHUP)
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

func flagSet(name string) (set bool) {
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}
