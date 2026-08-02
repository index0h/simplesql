package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func newRunner(
	t *testing.T,
	p Parser,
	gen Generator,
	cfg *Config,
	cfgPath string,
) *Runner {
	t.Helper()
	return NewRunner(p, gen, cfg, ConfigPath(cfgPath), zap.NewNop())
}

func TestRunner_Run_SingleRepository(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/UserRepository": {Output: "out.go"},
		},
	}

	iface := &Interface{Name: "UserRepository", PackageName: "pkg"}
	src := []byte("package pkg\n")

	mockParser.EXPECT().
		ParseInterface(filepath.Join(dir, "pkg"), "UserRepository").
		Return(iface, nil)
	mockGen.EXPECT().
		Generate("pkg", []*Request{{Interface: iface}}).
		Return(src, nil)

	require.NoError(t, newRunner(t, mockParser, mockGen, cfg, cfgPath).Run())

	got, err := os.ReadFile(filepath.Join(dir, "out.go"))
	require.NoError(t, err)
	require.Equal(t, src, got)
}

func TestRunner_Run_MultipleRepositoriesToSameFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/UserRepository": {Output: "out.go"},
			"pkg/PostRepository": {Output: "out.go"},
		},
	}

	userIface := &Interface{Name: "UserRepository", PackageName: "pkg"}
	postIface := &Interface{Name: "PostRepository", PackageName: "pkg"}
	src := []byte("package pkg\n")

	mockParser.EXPECT().ParseInterface(filepath.Join(dir, "pkg"), "UserRepository").Return(userIface, nil)
	mockParser.EXPECT().ParseInterface(filepath.Join(dir, "pkg"), "PostRepository").Return(postIface, nil)
	mockGen.EXPECT().
		Generate("pkg", gomock.Len(2)).
		Return(src, nil)

	require.NoError(t, newRunner(t, mockParser, mockGen, cfg, cfgPath).Run())
}

// TestRunner_Run_DeterministicOrderForSharedOutputFile guards against a regression where
// requests for a shared output file were built by iterating cfg.Repositories (a Go map,
// whose iteration order is randomized), so regenerating an unchanged config could reorder
// structs/methods in the output file for no reason. Repository keys are deliberately listed
// out of alphabetical order here to catch that.
func TestRunner_Run_DeterministicOrderForSharedOutputFile(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/GammaRepo": {Output: "out.go"},
			"pkg/AlphaRepo": {Output: "out.go"},
			"pkg/BetaRepo":  {Output: "out.go"},
		},
	}

	alphaIface := &Interface{Name: "AlphaRepo", PackageName: "pkg"}
	betaIface := &Interface{Name: "BetaRepo", PackageName: "pkg"}
	gammaIface := &Interface{Name: "GammaRepo", PackageName: "pkg"}

	mockParser.EXPECT().ParseInterface(filepath.Join(dir, "pkg"), "AlphaRepo").Return(alphaIface, nil)
	mockParser.EXPECT().ParseInterface(filepath.Join(dir, "pkg"), "BetaRepo").Return(betaIface, nil)
	mockParser.EXPECT().ParseInterface(filepath.Join(dir, "pkg"), "GammaRepo").Return(gammaIface, nil)

	var gotOrder []string
	mockGen.EXPECT().
		Generate("pkg", gomock.Any()).
		DoAndReturn(func(_ string, requests []*Request) ([]byte, error) {
			for _, req := range requests {
				gotOrder = append(gotOrder, req.Interface.Name)
			}
			return []byte("package pkg\n"), nil
		})

	require.NoError(t, newRunner(t, mockParser, mockGen, cfg, cfgPath).Run())
	require.Equal(t, []string{"AlphaRepo", "BetaRepo", "GammaRepo"}, gotOrder)
}

func TestRunner_Run_NestedOutputDir(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/UserRepository": {Output: "gen/out.go"},
		},
	}

	iface := &Interface{Name: "UserRepository", PackageName: "pkg"}
	src := []byte("package pkg\n")

	mockParser.EXPECT().ParseInterface(gomock.Any(), "UserRepository").Return(iface, nil)
	mockGen.EXPECT().Generate("pkg", gomock.Any()).Return(src, nil)

	require.NoError(t, newRunner(t, mockParser, mockGen, cfg, cfgPath).Run())

	got, err := os.ReadFile(filepath.Join(dir, "gen", "out.go"))
	require.NoError(t, err)
	require.Equal(t, src, got)
}

func TestRunner_Run_InvalidKey(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"NoSlashKey": {Output: "out.go"},
		},
	}

	err := newRunner(t, mockParser, mockGen, cfg, filepath.Join(dir, "config.yaml")).Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "NoSlashKey")
}

func TestRunner_Run_ParseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	parseErr := errors.New("parse failed")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/UserRepository": {Output: "out.go"},
		},
	}

	mockParser.EXPECT().ParseInterface(gomock.Any(), "UserRepository").Return(nil, parseErr)

	err := newRunner(t, mockParser, mockGen, cfg, filepath.Join(dir, "config.yaml")).Run()
	require.Error(t, err)
	require.ErrorIs(t, err, parseErr)
}

func TestRunner_Run_GenerateError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	genErr := errors.New("generate failed")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/UserRepository": {Output: "out.go"},
		},
	}

	iface := &Interface{Name: "UserRepository", PackageName: "pkg"}
	mockParser.EXPECT().ParseInterface(gomock.Any(), "UserRepository").Return(iface, nil)
	mockGen.EXPECT().Generate("pkg", gomock.Any()).Return(nil, genErr)

	err := newRunner(t, mockParser, mockGen, cfg, filepath.Join(dir, "config.yaml")).Run()
	require.Error(t, err)
	require.ErrorIs(t, err, genErr)
}

func TestRunner_Run_CustomStructName(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockParser := NewMockParser(ctrl)
	mockGen := NewMockGenerator(ctrl)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Dialect: DialectMySQL,
		Repositories: map[string]*RepositoryEntry{
			"pkg/UserRepository": {Output: "out.go", StructName: "myUserRepo"},
		},
	}

	iface := &Interface{Name: "UserRepository", PackageName: "pkg"}
	src := []byte("package pkg\n")

	mockParser.EXPECT().ParseInterface(gomock.Any(), "UserRepository").Return(iface, nil)
	mockGen.EXPECT().
		Generate("pkg", []*Request{{Interface: iface, StructName: "myUserRepo"}}).
		Return(src, nil)

	require.NoError(t, newRunner(t, mockParser, mockGen, cfg, cfgPath).Run())
}
