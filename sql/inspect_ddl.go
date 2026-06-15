package sql

// Read-only introspection seam for the DDL statement nodes, the schema-statement
// counterpart to the Parts accessors for DML. Each Parts method returns a
// read-only snapshot of a statement's parts, with slices defensively copied, so
// analysis tools can inspect a DDL node fully without the package freezing its
// field layout as API or letting callers mutate a built statement.

// CreateSchemaParts is a read-only snapshot of a CreateSchemaStmt.
type CreateSchemaParts struct {
	Name        string
	IfNotExists bool
}

// Parts returns a read-only snapshot of s.
func (s CreateSchemaStmt) Parts() CreateSchemaParts {
	return CreateSchemaParts{Name: s.name, IfNotExists: s.ifNotExists}
}

// DropSchemaParts is a read-only snapshot of a DropSchemaStmt.
type DropSchemaParts struct {
	Name     string
	IfExists bool
	Cascade  bool
}

// Parts returns a read-only snapshot of s.
func (s DropSchemaStmt) Parts() DropSchemaParts {
	return DropSchemaParts{Name: s.name, IfExists: s.ifExists, Cascade: s.cascade}
}

// CreateTableParts is a read-only snapshot of every part of a CreateTableStmt.
type CreateTableParts struct {
	Table       Table
	Columns     []ColumnDef
	PrimaryKey  []string
	Checks      []TableCheck
	Uniques     []TableUnique
	ForeignKeys []ForeignKeyConstraint
	Comment     string
	IfNotExists bool
	Temporary   bool
}

// Parts returns a read-only snapshot of s.
func (s CreateTableStmt) Parts() CreateTableParts {
	return CreateTableParts{
		Table:       s.table,
		Columns:     cloneSlice(s.columns),
		PrimaryKey:  cloneSlice(s.primaryKey),
		Checks:      cloneSlice(s.checks),
		Uniques:     cloneSlice(s.uniques),
		ForeignKeys: cloneSlice(s.foreignKeys),
		Comment:     s.comment,
		IfNotExists: s.ifNotExists,
		Temporary:   s.temporary,
	}
}

// DropTableParts is a read-only snapshot of a DropTableStmt.
type DropTableParts struct {
	Tables   []Table
	IfExists bool
}

// Parts returns a read-only snapshot of s.
func (s DropTableStmt) Parts() DropTableParts {
	return DropTableParts{Tables: cloneSlice(s.tables), IfExists: s.ifExists}
}

// CreateViewParts is a read-only snapshot of a CreateViewStmt.
type CreateViewParts struct {
	Table     Table
	Query     Query
	Columns   []string
	OrReplace bool
}

// Parts returns a read-only snapshot of s.
func (s CreateViewStmt) Parts() CreateViewParts {
	return CreateViewParts{Table: s.table, Query: s.query, Columns: cloneSlice(s.columns), OrReplace: s.orReplace}
}

// DropViewParts is a read-only snapshot of a DropViewStmt.
type DropViewParts struct {
	Table    Table
	IfExists bool
	Cascade  bool
}

// Parts returns a read-only snapshot of s.
func (s DropViewStmt) Parts() DropViewParts {
	return DropViewParts{Table: s.table, IfExists: s.ifExists, Cascade: s.cascade}
}

// CreateIndexParts is a read-only snapshot of a CreateIndexStmt.
type CreateIndexParts struct {
	Name        string
	Table       Table
	Columns     []string
	Unique      bool
	IfNotExists bool
}

// Parts returns a read-only snapshot of s.
func (s CreateIndexStmt) Parts() CreateIndexParts {
	return CreateIndexParts{
		Name:        s.name,
		Table:       s.table,
		Columns:     cloneSlice(s.columns),
		Unique:      s.unique,
		IfNotExists: s.ifNotExists,
	}
}

// DropIndexParts is a read-only snapshot of a DropIndexStmt.
type DropIndexParts struct {
	Name  string
	Table Table
}

// Parts returns a read-only snapshot of s.
func (s DropIndexStmt) Parts() DropIndexParts {
	return DropIndexParts{Name: s.name, Table: s.table}
}

// TruncateParts is a read-only snapshot of a TruncateStmt.
type TruncateParts struct {
	Tables          []Table
	RestartIdentity bool
	Cascade         bool
}

// Parts returns a read-only snapshot of s.
func (s TruncateStmt) Parts() TruncateParts {
	return TruncateParts{Tables: cloneSlice(s.tables), RestartIdentity: s.restartIdentity, Cascade: s.cascade}
}

// DropForeignKeyParts is a read-only snapshot of a DropForeignKeyStmt.
type DropForeignKeyParts struct {
	Table Table
	Name  string
}

// Parts returns a read-only snapshot of s.
func (s DropForeignKeyStmt) Parts() DropForeignKeyParts {
	return DropForeignKeyParts{Table: s.table, Name: s.name}
}

// CommentOnTableParts is a read-only snapshot of a CommentOnTableStmt.
type CommentOnTableParts struct {
	Table   Table
	Comment string
}

// Parts returns a read-only snapshot of s.
func (s CommentOnTableStmt) Parts() CommentOnTableParts {
	return CommentOnTableParts{Table: s.table, Comment: s.comment}
}

// CommentOnColumnParts is a read-only snapshot of a CommentOnColumnStmt.
type CommentOnColumnParts struct {
	Table   Table
	Column  string
	Comment string
}

// Parts returns a read-only snapshot of s.
func (s CommentOnColumnStmt) Parts() CommentOnColumnParts {
	return CommentOnColumnParts{Table: s.table, Column: s.column, Comment: s.comment}
}

// AlterTableParts is a read-only snapshot of an AlterTableStmt: the table and
// the ordered alterations applied to it.
type AlterTableParts struct {
	Table       Table
	Alterations []Alteration
}

// Parts returns a read-only snapshot of s.
func (s AlterTableStmt) Parts() AlterTableParts {
	c := AlterTableParts{Table: s.table}
	if len(s.alterations) > 0 {
		c.Alterations = make([]Alteration, len(s.alterations))
		for i, a := range s.alterations {
			c.Alterations[i] = alterationView(a)
		}
	}
	return c
}

// AlterationKind identifies which ALTER TABLE action an Alteration describes.
type AlterationKind int

const (
	// AlterAddColumn is ADD COLUMN; Column is set.
	AlterAddColumn AlterationKind = iota
	// AlterDropColumn is DROP COLUMN; Name is the column.
	AlterDropColumn
	// AlterAddForeignKey is ADD CONSTRAINT ... FOREIGN KEY; ForeignKey is set.
	AlterAddForeignKey
	// AlterAddCheck is ADD CONSTRAINT ... CHECK; Name and Expression are set.
	AlterAddCheck
	// AlterAddUnique is ADD CONSTRAINT ... UNIQUE; Name (optional) and Columns are set.
	AlterAddUnique
	// AlterDropConstraint is DROP CONSTRAINT; Name is the constraint.
	AlterDropConstraint
	// AlterColumnType changes a column's type; Name and Type are set.
	AlterColumnType
	// AlterSetNotNull adds NOT NULL; Name is the column and NotNull is true.
	AlterSetNotNull
	// AlterDropNotNull drops NOT NULL; Name is the column and NotNull is false.
	AlterDropNotNull
	// AlterSetDefault sets a column default; Name and Expression are set.
	AlterSetDefault
	// AlterDropDefault drops a column default; Name is the column.
	AlterDropDefault
	// AlterRenameColumn renames a column; Name is the old name and NewName the new.
	AlterRenameColumn
	// AlterRenameTable renames the table; NewName is the new table name.
	AlterRenameTable
	// AlterRestateColumn restates a column's full definition; Column is set.
	AlterRestateColumn
	// AlterAddPrimaryKey adds a primary key; Columns are the key columns.
	AlterAddPrimaryKey
	// AlterDropPrimaryKey drops the primary key.
	AlterDropPrimaryKey
)

// Alteration is a read-only view of one ALTER TABLE action. Kind selects which
// fields are meaningful (see each AlterationKind).
type Alteration struct {
	Kind       AlterationKind
	Name       string                // column or constraint name; rename's old column name
	NewName    string                // rename target (column or table)
	Columns    []string              // AddUnique / AddPrimaryKey columns
	Column     *ColumnDef            // AddColumn / RestateColumn
	Type       ColumnType            // AlterColumnType
	Expression Expression            // AddCheck / SetDefault
	ForeignKey *ForeignKeyConstraint // AddForeignKey
	NotNull    bool                  // SetNotNull (true) / DropNotNull (false)
}

// alterationView converts an internal alteration into its public read-only view.
func alterationView(a alteration) Alteration {
	switch v := a.(type) {
	case addColumnAlt:
		col := v.col
		return Alteration{Kind: AlterAddColumn, Column: &col}
	case dropColumnAlt:
		return Alteration{Kind: AlterDropColumn, Name: v.name}
	case addForeignKeyAlt:
		fk := v.fk
		return Alteration{Kind: AlterAddForeignKey, ForeignKey: &fk}
	case addCheckAlt:
		return Alteration{Kind: AlterAddCheck, Name: v.name, Expression: v.expr}
	case addUniqueAlt:
		return Alteration{Kind: AlterAddUnique, Name: v.name, Columns: cloneSlice(v.columns)}
	case dropConstraintAlt:
		return Alteration{Kind: AlterDropConstraint, Name: v.name}
	case alterColumnTypeAlt:
		return Alteration{Kind: AlterColumnType, Name: v.name, Type: v.typ}
	case notNullAlt:
		if v.notNull {
			return Alteration{Kind: AlterSetNotNull, Name: v.name, NotNull: true}
		}
		return Alteration{Kind: AlterDropNotNull, Name: v.name}
	case defaultAlt:
		if v.expr != nil {
			return Alteration{Kind: AlterSetDefault, Name: v.name, Expression: v.expr}
		}
		return Alteration{Kind: AlterDropDefault, Name: v.name}
	case renameColumnAlt:
		return Alteration{Kind: AlterRenameColumn, Name: v.from, NewName: v.to}
	case renameTableAlt:
		return Alteration{Kind: AlterRenameTable, NewName: v.name}
	case restateColumnAlt:
		col := v.col
		return Alteration{Kind: AlterRestateColumn, Column: &col}
	case addPrimaryKeyAlt:
		return Alteration{Kind: AlterAddPrimaryKey, Columns: cloneSlice(v.columns)}
	case dropPrimaryKeyAlt:
		return Alteration{Kind: AlterDropPrimaryKey}
	default:
		return Alteration{}
	}
}
