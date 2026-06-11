// Package typeconv maps database column types to Go types.
package typeconv

import "github.com/sqlkit-dev/sqlkit/internal/genmodel"

// GoType describes a generated Go type.
type GoType struct {
	ImportPath string
	Name       string
	Pointer    bool
}

// Converter maps a database column to a Go type.
type Converter interface {
	Convert(column genmodel.Column) (GoType, error)
}
