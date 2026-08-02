package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/index0h/simplesql/internal"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: simplesql <config.yaml>")
		os.Exit(1)
	}
	cfgPath := os.Args[1]

	log, err := zap.NewProduction()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "simplesql: create logger: %+v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	cfg, err := internal.Load(cfgPath)
	if err != nil {
		log.Error("simplesql failed", zap.Error(err))
		os.Exit(1)
	}

	reader := internal.NewReader(log)
	parser := internal.NewParser(log, reader)
	compiler := internal.NewCompiler(cfg.Dialect)
	gen := internal.NewGenerator(log, compiler)
	runner := internal.NewRunner(parser, gen, cfg, internal.ConfigPath(cfgPath), log)

	if err := runner.Run(); err != nil {
		log.Error("simplesql failed", zap.Error(err))
		os.Exit(1)
	}
}
