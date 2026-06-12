package rowset

// Field describes one column in a RowSet.
type Field struct {
	Name         string
	DatabaseType string
	GoType       string
	Nullable     bool
}
