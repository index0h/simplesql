package internal

import (
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

// minimalBody is a valid CompileBody output that defines _query and _args
// so that any generated method template produces valid Go source.
const minimalBody = "\tvar _sb strings.Builder\n\tvar _args []any\n\t_query := _sb.String()\n"

func purgeMethod() Method {
	return Method{
		Name:       "Purge",
		SQL:        "DELETE FROM t",
		QueryKind:  QueryDelete,
		ReturnKind: ReturnNothing,
	}
}

func purgeIface() *Interface {
	return &Interface{
		Name:        "Repo",
		PackageName: "pkg",
		Methods:     []Method{purgeMethod()},
	}
}

func TestGenerator_Generate_CompileBodyError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCompiler := NewMockCompiler(ctrl)

	compileErr := errors.New("tokenise failed")
	mockCompiler.EXPECT().CompileBody(gomock.Any()).Return("", false, compileErr)

	gen := NewGenerator(zap.NewNop(), mockCompiler)
	_, err := gen.Generate("pkg", []*Request{{Interface: purgeIface()}})
	require.Error(t, err)
	require.ErrorIs(t, err, compileErr)
}

func TestGenerator_Generate_StrconvImportAdded(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCompiler := NewMockCompiler(ctrl)

	mockCompiler.EXPECT().CompileBody(gomock.Any()).Return(minimalBody, true, nil)

	gen := NewGenerator(zap.NewNop(), mockCompiler)
	src, err := gen.Generate("pkg", []*Request{{Interface: purgeIface()}})
	require.NoError(t, err)
	require.Contains(t, string(src), `"strconv"`)
}

func TestGenerator_Generate_StrconvImportAbsent(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCompiler := NewMockCompiler(ctrl)

	mockCompiler.EXPECT().CompileBody(gomock.Any()).Return(minimalBody, false, nil)

	gen := NewGenerator(zap.NewNop(), mockCompiler)
	src, err := gen.Generate("pkg", []*Request{{Interface: purgeIface()}})
	require.NoError(t, err)
	require.NotContains(t, string(src), `"strconv"`)
}

func TestGenerator_Generate_DefaultStructName(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCompiler := NewMockCompiler(ctrl)

	mockCompiler.EXPECT().CompileBody(gomock.Any()).Return(minimalBody, false, nil)

	gen := NewGenerator(zap.NewNop(), mockCompiler)
	src, err := gen.Generate("pkg", []*Request{{Interface: purgeIface()}})
	require.NoError(t, err)
	require.Contains(t, string(src), "repoImpl")
	require.Contains(t, string(src), "func NewRepo(")
}

func TestGenerator_Generate_ExplicitStructName(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCompiler := NewMockCompiler(ctrl)

	mockCompiler.EXPECT().CompileBody(gomock.Any()).Return(minimalBody, false, nil)

	gen := NewGenerator(zap.NewNop(), mockCompiler)
	src, err := gen.Generate("pkg", []*Request{{
		Interface:  purgeIface(),
		StructName: "myRepo",
	}})
	require.NoError(t, err)
	require.Contains(t, string(src), "myRepo")
}

func TestGenerator_Generate_StrconvTrackedAcrossMethods(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCompiler := NewMockCompiler(ctrl)

	iface := &Interface{
		Name:        "Repo",
		PackageName: "pkg",
		Methods: []Method{
			{Name: "A", SQL: "DELETE FROM a", QueryKind: QueryDelete, ReturnKind: ReturnNothing},
			{Name: "B", SQL: "DELETE FROM b", QueryKind: QueryDelete, ReturnKind: ReturnNothing},
		},
	}

	// Only the second method needs strconv.
	gomock.InOrder(
		mockCompiler.EXPECT().CompileBody(gomock.Any()).Return(minimalBody, false, nil),
		mockCompiler.EXPECT().CompileBody(gomock.Any()).Return(minimalBody, true, nil),
	)

	gen := NewGenerator(zap.NewNop(), mockCompiler)
	src, err := gen.Generate("pkg", []*Request{{Interface: iface}})
	require.NoError(t, err)
	require.Contains(t, string(src), `"strconv"`)
}
