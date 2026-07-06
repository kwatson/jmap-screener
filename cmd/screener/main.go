// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/kwatson/jmap-screener/internal/config"
	"github.com/kwatson/jmap-screener/internal/monitor"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to config file")
	flag.Parse()
	configExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})

	cfg, err := config.LoadWithOptions(*configPath, configExplicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := monitor.Run(cfg); err != nil {
		slog.Error("monitor failed", "error", err)
		os.Exit(1)
	}
}
