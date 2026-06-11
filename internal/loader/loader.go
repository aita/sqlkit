// Package loader defines database metadata loading contracts.
package loader

import (
	"context"

	"github.com/sqlkit-dev/sqlkit/internal/genmodel"
)

// Loader reads a database schema into the generator model.
type Loader interface {
	Load(ctx context.Context) (genmodel.Schema, error)
}

// Options configures metadata loading.
type Options struct {
	Driver string
	DSN    string
	Schema string
}
