package sql

// Read-only introspection seam for the statement nodes added alongside the core
// DDL: privileges, functions, extensions, enum types, sequences, renames,
// materialized views, and transaction control. Each Parts method returns a
// read-only snapshot with slices defensively copied, matching inspect_ddl.go.

// GrantParts is a read-only snapshot of a GrantStmt.
type GrantParts struct {
	Privileges      []string
	ObjectType      string
	Objects         []string
	Grantees        []string
	WithGrantOption bool
}

// Parts returns a read-only snapshot of s.
func (s GrantStmt) Parts() GrantParts {
	return GrantParts{
		Privileges:      cloneSlice(s.privileges),
		ObjectType:      s.objectType,
		Objects:         cloneSlice(s.objects),
		Grantees:        cloneSlice(s.grantees),
		WithGrantOption: s.withGrantOption,
	}
}

// RevokeParts is a read-only snapshot of a RevokeStmt. Behavior is "", "CASCADE",
// or "RESTRICT".
type RevokeParts struct {
	GrantOptionFor bool
	Privileges     []string
	ObjectType     string
	Objects        []string
	Grantees       []string
	Behavior       string
}

// Parts returns a read-only snapshot of s.
func (s RevokeStmt) Parts() RevokeParts {
	return RevokeParts{
		GrantOptionFor: s.grantOptionFor,
		Privileges:     cloneSlice(s.privileges),
		ObjectType:     s.objectType,
		Objects:        cloneSlice(s.objects),
		Grantees:       cloneSlice(s.grantees),
		Behavior:       s.behavior,
	}
}

// CreateFunctionParts is a read-only snapshot of a CreateFunctionStmt.
type CreateFunctionParts struct {
	Name      string
	OrReplace bool
	Args      []FunctionArg
	Returns   string
	Language  string
	Options   []string
	Body      string
}

// Parts returns a read-only snapshot of s.
func (s CreateFunctionStmt) Parts() CreateFunctionParts {
	return CreateFunctionParts{
		Name:      s.name,
		OrReplace: s.orReplace,
		Args:      cloneSlice(s.args),
		Returns:   s.returns,
		Language:  s.language,
		Options:   cloneSlice(s.options),
		Body:      s.body,
	}
}

// DropFunctionParts is a read-only snapshot of a DropFunctionStmt. Behavior is
// "", "CASCADE", or "RESTRICT". HasArgs reports whether an argument-type list
// was given (an empty list still renders "()").
type DropFunctionParts struct {
	Name     string
	IfExists bool
	HasArgs  bool
	ArgTypes []string
	Behavior string
}

// Parts returns a read-only snapshot of s.
func (s DropFunctionStmt) Parts() DropFunctionParts {
	return DropFunctionParts{
		Name:     s.name,
		IfExists: s.ifExists,
		HasArgs:  s.hasArgs,
		ArgTypes: cloneSlice(s.argTypes),
		Behavior: s.behavior,
	}
}

// CreateExtensionParts is a read-only snapshot of a CreateExtensionStmt.
type CreateExtensionParts struct {
	Name        string
	IfNotExists bool
	Schema      string
}

// Parts returns a read-only snapshot of s.
func (s CreateExtensionStmt) Parts() CreateExtensionParts {
	return CreateExtensionParts{Name: s.name, IfNotExists: s.ifNotExists, Schema: s.schema}
}

// DropExtensionParts is a read-only snapshot of a DropExtensionStmt.
type DropExtensionParts struct {
	Names    []string
	IfExists bool
	Cascade  bool
}

// Parts returns a read-only snapshot of s.
func (s DropExtensionStmt) Parts() DropExtensionParts {
	return DropExtensionParts{Names: cloneSlice(s.names), IfExists: s.ifExists, Cascade: s.cascade}
}

// CreateEnumParts is a read-only snapshot of a CreateEnumStmt.
type CreateEnumParts struct {
	Name   string
	Values []string
}

// Parts returns a read-only snapshot of s.
func (s CreateEnumStmt) Parts() CreateEnumParts {
	return CreateEnumParts{Name: s.name, Values: cloneSlice(s.values)}
}

// DropTypeParts is a read-only snapshot of a DropTypeStmt.
type DropTypeParts struct {
	Names    []string
	IfExists bool
	Cascade  bool
}

// Parts returns a read-only snapshot of s.
func (s DropTypeStmt) Parts() DropTypeParts {
	return DropTypeParts{Names: cloneSlice(s.names), IfExists: s.ifExists, Cascade: s.cascade}
}

// AlterTypeAddValueParts is a read-only snapshot of an AlterTypeAddValueStmt.
// At most one of Before/After is set.
type AlterTypeAddValueParts struct {
	Name        string
	Value       string
	IfNotExists bool
	Before      string
	After       string
}

// Parts returns a read-only snapshot of s.
func (s AlterTypeAddValueStmt) Parts() AlterTypeAddValueParts {
	return AlterTypeAddValueParts{
		Name:        s.name,
		Value:       s.value,
		IfNotExists: s.ifNotExists,
		Before:      s.before,
		After:       s.after,
	}
}

// AlterTypeRenameValueParts is a read-only snapshot of an AlterTypeRenameValueStmt.
type AlterTypeRenameValueParts struct {
	Name     string
	OldValue string
	NewValue string
}

// Parts returns a read-only snapshot of s.
func (s AlterTypeRenameValueStmt) Parts() AlterTypeRenameValueParts {
	return AlterTypeRenameValueParts{Name: s.name, OldValue: s.oldValue, NewValue: s.newValue}
}

// CreateSequenceParts is a read-only snapshot of a CreateSequenceStmt. A nil
// numeric pointer means the option is absent; Cycle is nil when neither CYCLE
// nor NO CYCLE was set. OwnedBy is "", "NONE", or "table.column".
type CreateSequenceParts struct {
	Sequence    Table
	IfNotExists bool
	Increment   *int64
	MinValue    *int64
	NoMinValue  bool
	MaxValue    *int64
	NoMaxValue  bool
	Start       *int64
	Cache       *int64
	Cycle       *bool
	OwnedBy     string
}

// Parts returns a read-only snapshot of s.
func (s CreateSequenceStmt) Parts() CreateSequenceParts {
	c := CreateSequenceParts{
		Sequence:    s.sequence,
		IfNotExists: s.ifNotExists,
		Increment:   cloneIntPtr(s.increment),
		MinValue:    cloneIntPtr(s.minValue),
		NoMinValue:  s.noMinValue,
		MaxValue:    cloneIntPtr(s.maxValue),
		NoMaxValue:  s.noMaxValue,
		Start:       cloneIntPtr(s.start),
		Cache:       cloneIntPtr(s.cache),
		OwnedBy:     s.ownedBy,
	}
	if s.hasCycle {
		cycle := s.cycle
		c.Cycle = &cycle
	}
	return c
}

// DropSequenceParts is a read-only snapshot of a DropSequenceStmt.
type DropSequenceParts struct {
	Names    []string
	IfExists bool
	Cascade  bool
}

// Parts returns a read-only snapshot of s.
func (s DropSequenceStmt) Parts() DropSequenceParts {
	return DropSequenceParts{Names: cloneSlice(s.names), IfExists: s.ifExists, Cascade: s.cascade}
}

// RenameTableParts is a read-only snapshot of a RenameTableStmt.
type RenameTableParts struct {
	Table   Table
	NewName string
}

// Parts returns a read-only snapshot of s.
func (s RenameTableStmt) Parts() RenameTableParts {
	return RenameTableParts{Table: s.table, NewName: s.newName}
}

// RenameColumnParts is a read-only snapshot of a RenameColumnStmt.
type RenameColumnParts struct {
	Table   Table
	OldName string
	NewName string
}

// Parts returns a read-only snapshot of s.
func (s RenameColumnStmt) Parts() RenameColumnParts {
	return RenameColumnParts{Table: s.table, OldName: s.oldName, NewName: s.newName}
}

// CreateMaterializedViewParts is a read-only snapshot of a
// CreateMaterializedViewStmt.
type CreateMaterializedViewParts struct {
	Table       Table
	Query       Query
	Columns     []string
	IfNotExists bool
	WithNoData  bool
}

// Parts returns a read-only snapshot of s.
func (s CreateMaterializedViewStmt) Parts() CreateMaterializedViewParts {
	return CreateMaterializedViewParts{
		Table:       s.table,
		Query:       s.query,
		Columns:     cloneSlice(s.columns),
		IfNotExists: s.ifNotExists,
		WithNoData:  s.withNoData,
	}
}

// DropMaterializedViewParts is a read-only snapshot of a DropMaterializedViewStmt.
type DropMaterializedViewParts struct {
	Table    Table
	IfExists bool
	Cascade  bool
}

// Parts returns a read-only snapshot of s.
func (s DropMaterializedViewStmt) Parts() DropMaterializedViewParts {
	return DropMaterializedViewParts{Table: s.table, IfExists: s.ifExists, Cascade: s.cascade}
}

// RefreshMaterializedViewParts is a read-only snapshot of a
// RefreshMaterializedViewStmt.
type RefreshMaterializedViewParts struct {
	Table        Table
	Concurrently bool
	WithNoData   bool
}

// Parts returns a read-only snapshot of s.
func (s RefreshMaterializedViewStmt) Parts() RefreshMaterializedViewParts {
	return RefreshMaterializedViewParts{Table: s.table, Concurrently: s.concurrent, WithNoData: s.withNoData}
}

// TransactionParts is a read-only snapshot of a TransactionStmt. Kind is the
// statement keyword (e.g. "BEGIN", "SAVEPOINT"); Savepoint names the savepoint
// for the savepoint forms.
type TransactionParts struct {
	Kind      string
	Savepoint string
}

// Parts returns a read-only snapshot of s.
func (s TransactionStmt) Parts() TransactionParts {
	return TransactionParts{Kind: s.kind, Savepoint: s.savepoint}
}

// cloneIntPtr copies an optional int64 so a snapshot never shares the pointer
// with the statement it came from.
func cloneIntPtr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
