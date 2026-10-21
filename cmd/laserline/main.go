package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"laserline/internal/hub"
	"laserline/internal/invite"
	"laserline/internal/transfer"
	"laserline/internal/tui"
)

const version = "0.6.0"

func usage() {
	fmt.Print(`Laserline DX - encrypted BBS-like file sharing and chat (v` + version + `)

Usage:
  laserline serve [options] [file-or-directory]
  laserline connect [options] CODE

Commands:
  serve     Share one file, one directory recursively, or (by default) .
  connect   Join Laserline for BBS-like browsing, resumable downloads and chat.

Examples:
  laserline serve
  laserline serve ~/Downloads
  laserline serve --listen :47477 --advertise 192.168.1.50
  laserline connect LD1-... 
  laserline connect --nick Alice LD1-...

All network traffic uses TLS 1.3. Direct connectivity is required; for
Internet access use port forwarding or a VPN such as Tailscale.
`)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Laserline DX:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		usage()
		return nil
	}
	switch os.Args[1] {
	case "serve":
		return serve(os.Args[2:])
	case "connect":
		return connect(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return nil
	case "version", "--version":
		fmt.Println("Laserline DX", version)
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", ":47477", "TCP bind address")
	advertise := fs.String("advertise", "", "reachable IP address or hostname to put in the code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("serve accepts at most one file or directory")
	}
	share := "."
	if fs.NArg() == 1 {
		share = fs.Arg(0)
	}
	srv, err := hub.Start(hub.Options{Path: share, Listen: *listen, Advertise: *advertise})
	if err != nil {
		return err
	}
	defer srv.Close()
	abs, _ := filepath.Abs(share)
	fmt.Printf("\n  ✦ LASERLINE DX %s  /  ENCRYPTED BBS-LIKE FILE + CHAT\n\n", version)
	fmt.Printf("  Sharing: %s\n  Listening: %s\n  Invitation code (share privately):\n\n%s\n\n", abs, srv.Address(), srv.Invitation())
	fmt.Println("  Ask the recipient to run: laserline connect CODE")
	fmt.Println("  TLS 1.3 · multiple users · resumable downloads · Ctrl+C to stop")
	fmt.Println("  Code expires when this server process stops.")
	fmt.Println()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nLaserline DX server stopped")
			return nil
		case e := <-srv.Events:
			fmt.Println("  • " + e)
		}
	}
}
func connect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	nick := fs.String("nick", "Guest", "chat nickname")
	downloads := fs.String("downloads", "", "directory for downloaded files (default: ./LaserlineDownloads)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("connect needs exactly one invitation CODE")
	}
	code, err := invite.Parse(fs.Arg(0))
	if err != nil {
		return err
	}
	if strings.TrimSpace(*downloads) == "" {
		*downloads = "LaserlineDownloads"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client, welcome, err := transfer.Join(ctx, code, *nick)
	if err != nil {
		return err
	}
	defer client.Close()
	return tui.Run(ctx, client, welcome, *downloads)
}
