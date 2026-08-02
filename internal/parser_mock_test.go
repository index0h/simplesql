package internal

import (
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

func TestParser_ParseInterface_ReadDirError(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockReader := NewMockReader(ctrl)

	readErr := errors.New("disk error")
	mockReader.EXPECT().ReadDir("/some/dir").Return(nil, nil, readErr)

	p := NewParser(zap.NewNop(), mockReader)
	_, err := p.ParseInterface("/some/dir", "Repo")
	require.Error(t, err)
	require.ErrorIs(t, err, readErr)
}

func TestParser_ParseInterface_EmptyDir(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockReader := NewMockReader(ctrl)

	mockReader.EXPECT().ReadDir(gomock.Any()).Return(nil, nil, nil)

	p := NewParser(zap.NewNop(), mockReader)
	_, err := p.ParseInterface("/some/dir", "Missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Missing")
}
