package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

func TestLoad_DefaultsToMySQL(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
repositories:
  pkg/Repo: out.go
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, DialectMySQL, cfg.Dialect)
}

func TestLoad_AcceptsKnownDialects(t *testing.T) {
	for _, d := range []Dialect{DialectMySQL, DialectPostgres, DialectSQLite} {
		dir := t.TempDir()
		path := writeConfig(t, dir, "dialect: "+string(d)+"\nrepositories:\n  pkg/Repo: out.go\n")
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, d, cfg.Dialect)
	}
}

func TestLoad_RejectsUnknownDialect(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
dialect: postgress
repositories:
  pkg/Repo: out.go
`)
	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported dialect")
	require.Contains(t, err.Error(), "postgress")
}

func TestLoad_RejectsMissingOutput(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
dialect: mysql
repositories:
  pkg/Repo:
`)
	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing output")
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	require.Error(t, err)
}
