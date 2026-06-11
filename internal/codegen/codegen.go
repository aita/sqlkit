// Package codegen defines code generation contracts.
package codegen

import (
	"context"
	"io"

	"github.com/sqlkit-dev/sqlkit/internal/genmodel"
)

// Generator emits Go code for a loaded schema model.
type Generator interface {
	Generate(ctx context.Context, model genmodel.Schema, out io.Writer) error
}

// Options configures generation.
type Options struct {
	PackageName string
	ModulePath  string
}
