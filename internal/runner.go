package internal

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cockroachdb/errors"
	"go.uber.org/zap"
)

// ConfigPath is the path to the simplesql config file, provided for DI.
type ConfigPath string

// Runner orchestrates parsing and code generation for all repositories in a config.
type Runner struct {
	parser    Parser
	generator Generator
	cfg       *Config
	cfgPath   string
	log       *zap.Logger
}

// NewRunner returns a new Runner.
func NewRunner(p Parser, gen Generator, cfg *Config, cfgPath ConfigPath, log *zap.Logger) *Runner {
	return &Runner{
		parser:    p,
		generator: gen,
		cfg:       cfg,
		cfgPath:   string(cfgPath),
		log:       log,
	}
}

// Run processes all repositories from the config, writing generated files.
func (r *Runner) Run() error {
	r.log.Info("loaded config", zap.String("dialect", string(r.cfg.Dialect)), zap.String("path", r.cfgPath))

	cfgDir := filepath.Dir(r.cfgPath)

	type entry struct {
		dir           string
		interfaceName string
		structName    string
	}
	outputGroups := map[string][]entry{}

	// cfg.Repositories is a map, so its iteration order is randomized; process keys in
	// sorted order so that both the entries within one shared output file and the order
	// output files are generated in stay identical across runs. Otherwise, regenerating
	// an unchanged config could reorder structs/methods in the output file for no reason.
	keys := make([]string, 0, len(r.cfg.Repositories))
	for key := range r.cfg.Repositories {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var outFileOrder []string
	for _, key := range keys {
		repo := r.cfg.Repositories[key]
		dir, interfaceName, err := splitKey(key)
		if err != nil {
			return errors.Wrapf(err, "invalid repository key %q", key)
		}
		dir = filepath.Join(cfgDir, dir)
		outFile := filepath.Join(cfgDir, repo.Output)
		if _, ok := outputGroups[outFile]; !ok {
			outFileOrder = append(outFileOrder, outFile)
		}
		outputGroups[outFile] = append(outputGroups[outFile], entry{
			dir:           dir,
			interfaceName: interfaceName,
			structName:    repo.StructName,
		})
	}

	for _, outFile := range outFileOrder {
		entries := outputGroups[outFile]
		var requests []*Request
		var packageName string

		for _, e := range entries {
			parsedInterface, err := r.parser.ParseInterface(e.dir, e.interfaceName)
			if err != nil {
				return errors.Wrapf(err, "parse %s", e.interfaceName)
			}
			if packageName == "" {
				packageName = parsedInterface.PackageName
			}
			requests = append(requests, &Request{
				Interface:  parsedInterface,
				StructName: e.structName,
			})
		}

		src, err := r.generator.Generate(packageName, requests)
		if err != nil {
			return errors.Wrapf(err, "generate %s", outFile)
		}

		err = os.MkdirAll(filepath.Dir(outFile), 0755)
		if err != nil {
			return errors.Wrap(err, "create output directory")
		}
		err = os.WriteFile(outFile, src, 0644)
		if err != nil {
			return errors.Wrapf(err, "write %s", outFile)
		}
		r.log.Info("generated", zap.String("file", outFile))
	}
	return nil
}

// splitKey splits "pkg/dir/InterfaceName" into ("pkg/dir", "InterfaceName").
func splitKey(key string) (dir, interfaceName string, err error) {
	key = filepath.ToSlash(key)
	idx := strings.LastIndex(key, "/")
	if idx < 0 {
		return "", "", errors.WithStack(errors.Newf("key must be <dir>/<InterfaceName>, got %q", key))
	}
	return key[:idx], key[idx+1:], nil
}
