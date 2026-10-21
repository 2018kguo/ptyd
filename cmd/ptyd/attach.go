package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/2018kguo/ptyd/internal/client"
)

func defaultAddr() string {
	if a := os.Getenv("PTYD_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:4242"
}

// runAttach implements `ptyd attach [flags] <id>`.
func runAttach(args []string) int {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	addr := fs.String("addr", defaultAddr(), "Daemon address host:port (default $PTYD_ADDR or 127.0.0.1:4242)")
	readOnly := fs.Bool("readonly", false, "Watch without sending input or resizing")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: ptyd attach [flags] <terminal-id>\n\nAttach this terminal to a ptyd terminal. Detach with %s.\nThe token is read from $PTYD_TOKEN.\n\n", client.DetachKeyHelp)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	msg, err := client.Attach(ctx, client.Options{
		Addr: *addr, Token: os.Getenv("PTYD_TOKEN"), ID: fs.Arg(0), ReadOnly: *readOnly,
	}, os.Stdin, os.Stdout, winch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ptyd: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "\r\n[ptyd: %s]\r\n", msg)
	return 0
}
