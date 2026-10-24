// Command seedtensor runs Phase 1: a local TensorFold inference engine that seeds
// its own verified model weights over BitTorrent while idle.
//
// Subcommands:
//
//	seedtensor init   [-config path] [-model id]   write a starter config
//	seedtensor verify [-config path]               verify weights against the record
//	seedtensor run    [-config path]               resolve, verify, serve, and seed
//
// Phase 2 (embedding the security harness) is deliberately not here.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Alexintosh/SeedTensor/internal/config"
	"github.com/Alexintosh/SeedTensor/internal/model"
	"github.com/Alexintosh/SeedTensor/internal/supervisor"
)

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		os.Exit(cmdInit(os.Args[2:]))
	case "verify":
		os.Exit(cmdVerify(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `seedtensor — local inference + idle model seeding (Phase 1)

usage:
  seedtensor init   [-config path] [-model id]   write a starter config file
  seedtensor verify [-config path]               verify weights against the checksum record
  seedtensor run    [-config path]               resolve, verify, serve inference, and seed

flags default: -config ./seedtensor.json
`)
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	path := fs.String("config", "seedtensor.json", "config file path to write")
	modelID := fs.String("model", "", "model id (e.g. TensorFold/Qwen3.8-27B-oQ4)")
	fs.Parse(args)

	cfg := config.Default()
	if *modelID != "" {
		cfg.Model.ID = *modelID
	} else {
		cfg.Model.ID = "TensorFold/Qwen3.8-27B-oQ4"
	}
	if _, err := os.Stat(*path); err == nil {
		fmt.Fprintf(os.Stderr, "refusing to overwrite existing %s\n", *path)
		return 1
	}
	if err := config.Save(*path, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("wrote %s (edit model.id and seed settings, then `seedtensor run`)\n", *path)
	return 0
}

func cmdVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	path := fs.String("config", "seedtensor.json", "config file path")
	fs.Parse(args)

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resolver, err := model.NewResolver(cfg.Model)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resolved, err := resolver.Resolve(cfg.Model.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve:", err)
		return 1
	}
	fmt.Printf("verifying %s against %s (%d files)\n", resolved.ID, resolved.Origin, len(resolved.Files))
	res, err := model.Verify(cfg.Paths.WeightsDir, resolved, printProgress())
	fmt.Println()
	if err != nil {
		for _, p := range res.Problems {
			fmt.Fprintln(os.Stderr, "  problem:", p)
		}
		fmt.Fprintln(os.Stderr, "VERIFICATION FAILED")
		return 1
	}
	fmt.Printf("OK: %d files verified\n", res.Checked)
	return 0
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	path := fs.String("config", "seedtensor.json", "config file path")
	fs.Parse(args)

	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	deps, err := supervisor.Production(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r := supervisor.New(cfg, deps)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := r.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, "run:", err)
		return 1
	}
	return 0
}

func printProgress() func(done, total int64) {
	last := -1
	return func(done, total int64) {
		if total == 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct > 100 {
			pct = 100
		}
		if pct != last {
			fmt.Printf("\rverifying... %d%%", pct)
			last = pct
		}
	}
}
