package internal

import (
	uberconfig "go.uber.org/config"

	"github.com/cockroachdb/errors"
)

type Dialect string

const (
	DialectMySQL    Dialect = "mysql"
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

type RepositoryEntry struct {
	Output     string `yaml:"output"`
	StructName string `yaml:"struct_name"`
}

// UnmarshalYAML supports shorthand ("output.go") and full struct form.
func (r *RepositoryEntry) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var output string
	err := unmarshal(&output)
	if err == nil && output != "" {
		r.Output = output
		return nil
	}
	type plain RepositoryEntry
	return unmarshal((*plain)(r))
}

type Config struct {
	Dialect      Dialect                     `yaml:"dialect"`
	Repositories map[string]*RepositoryEntry `yaml:"repositories"`
}

func Load(path string) (*Config, error) {
	provider, err := uberconfig.NewYAML(uberconfig.File(path))
	if err != nil {
		return nil, errors.Wrap(err, "load config file")
	}

	var cfg Config
	err = provider.Get(uberconfig.Root).Populate(&cfg)
	if err != nil {
		return nil, errors.Wrap(err, "parse config file")
	}

	if cfg.Dialect == "" {
		cfg.Dialect = DialectMySQL
	}

	switch cfg.Dialect {
	case DialectMySQL, DialectPostgres, DialectSQLite:
	default:
		return nil, errors.WithStack(errors.Newf(
			"unsupported dialect %q: expected %q, %q, or %q", cfg.Dialect, DialectMySQL, DialectPostgres, DialectSQLite))
	}

	for key, entry := range cfg.Repositories {
		if entry == nil || entry.Output == "" {
			return nil, errors.WithStack(errors.Newf("repository %q missing output", key))
		}
	}

	return &cfg, nil
}
