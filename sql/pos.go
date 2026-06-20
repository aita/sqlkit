package sql

// NoPos is what Pos reports for a node with no recorded source position — one
// built with the query builders rather than parsed. It is -1 so that byte offset
// 0 (the very start of the source, where a top-level statement begins) is a real,
// distinct position rather than colliding with "no position".
const NoPos = -1

// Node is an AST node that can report where it was parsed from: the 0-based byte
// offset of its start in the source SQL, or NoPos when it was built with the query
// builders rather than parsed. The compiler ignores positions; they are metadata,
// so two queries that differ only in their nodes' offsets compile identically.
//
// Only the byte offset is kept. A line and column are derived from the original
// SQL text downstream (a linter, a diagnostic) by scanning to the offset, so a
// node need not carry them.
type Node interface{ Pos() int }

// posField is embedded in AST node structs to carry a source position and satisfy
// Node. NodePos stores the byte offset plus one, so the zero value (an unparsed,
// builder-built node) is NoPos while byte offset 0 is still representable. The
// parser records it through its own helpers; the field is exported so the parser,
// in another package, can set it on a node it has just built.
type posField struct{ NodePos int }

// Pos returns the node's 0-based byte offset in the source SQL, or NoPos when it
// was built rather than parsed.
func (p posField) Pos() int { return p.NodePos - 1 }

// SetPos records pos as the source position of q's top-level statement node and
// returns q. It is the seam a parser uses to attach a statement's offset:
// PostgreSQL records that offset on the RawStmt wrapper, not on the statement
// node, and the builders keep their statement in an unexported field, so it
// cannot be set from outside the package. A query kind that carries no position
// (a raw passthrough) is returned unchanged.
func SetPos(q Query, pos int) Query {
	if s, ok := q.(posSetter); ok {
		return s.setPos(pos)
	}
	return q
}

// posSetter is implemented by every statement node and its fluent builder: a
// value that can record a top-level source position and return itself. A
// builder sets the offset on the statement it wraps (its unexported field), a
// statement value on its own embedded posField. SetPos uses it to attach a
// parsed statement's offset uniformly, without enumerating every concrete kind
// at the call site or knowing which one it holds. The methods live here, beside
// SetPos, so the whole position-setting seam reads in one place.
type posSetter interface {
	Query
	setPos(pos int) Query
}

// DML statements and their builders.

func (b SelectBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }
func (b CompoundBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
func (b InsertBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }
func (b UpdateBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }
func (b DeleteBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }
func (b MergeBuilder) setPos(pos int) Query    { b.s.NodePos = pos; return b }

func (s SelectStmt) setPos(pos int) Query   { s.NodePos = pos; return s }
func (s CompoundStmt) setPos(pos int) Query { s.NodePos = pos; return s }
func (s InsertStmt) setPos(pos int) Query   { s.NodePos = pos; return s }
func (s UpdateStmt) setPos(pos int) Query   { s.NodePos = pos; return s }
func (s DeleteStmt) setPos(pos int) Query   { s.NodePos = pos; return s }
func (s MergeStmt) setPos(pos int) Query    { s.NodePos = pos; return s }
func (q ValuesQuery) setPos(pos int) Query  { q.NodePos = pos; return q }

// DDL statements and their builders.

func (b CreateSchemaBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
func (b DropSchemaBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }
func (b CreateTableBuilder) setPos(pos int) Query  { b.s.NodePos = pos; return b }
func (b DropTableBuilder) setPos(pos int) Query    { b.s.NodePos = pos; return b }
func (b CreateViewBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }
func (b DropViewBuilder) setPos(pos int) Query     { b.s.NodePos = pos; return b }
func (b CreateIndexBuilder) setPos(pos int) Query  { b.s.NodePos = pos; return b }
func (b TruncateBuilder) setPos(pos int) Query     { b.s.NodePos = pos; return b }
func (b AlterTableBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }

func (s CreateSchemaStmt) setPos(pos int) Query    { s.NodePos = pos; return s }
func (s DropSchemaStmt) setPos(pos int) Query      { s.NodePos = pos; return s }
func (s CreateTableStmt) setPos(pos int) Query     { s.NodePos = pos; return s }
func (s DropTableStmt) setPos(pos int) Query       { s.NodePos = pos; return s }
func (s CreateViewStmt) setPos(pos int) Query      { s.NodePos = pos; return s }
func (s DropViewStmt) setPos(pos int) Query        { s.NodePos = pos; return s }
func (s CreateIndexStmt) setPos(pos int) Query     { s.NodePos = pos; return s }
func (s DropIndexStmt) setPos(pos int) Query       { s.NodePos = pos; return s }
func (s TruncateStmt) setPos(pos int) Query        { s.NodePos = pos; return s }
func (s DropForeignKeyStmt) setPos(pos int) Query  { s.NodePos = pos; return s }
func (s CommentOnTableStmt) setPos(pos int) Query  { s.NodePos = pos; return s }
func (s CommentOnColumnStmt) setPos(pos int) Query { s.NodePos = pos; return s }
func (s AlterTableStmt) setPos(pos int) Query      { s.NodePos = pos; return s }

// Enum / type DDL.

func (b DropTypeBuilder) setPos(pos int) Query          { b.s.NodePos = pos; return b }
func (b AlterTypeAddValueBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

func (s CreateEnumStmt) setPos(pos int) Query           { s.NodePos = pos; return s }
func (s DropTypeStmt) setPos(pos int) Query             { s.NodePos = pos; return s }
func (s AlterTypeAddValueStmt) setPos(pos int) Query    { s.NodePos = pos; return s }
func (s AlterTypeRenameValueStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// Extension DDL.

func (b CreateExtensionBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
func (b DropExtensionBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }

func (s CreateExtensionStmt) setPos(pos int) Query { s.NodePos = pos; return s }
func (s DropExtensionStmt) setPos(pos int) Query   { s.NodePos = pos; return s }

// Materialized view DDL.

func (b CreateMaterializedViewBuilder) setPos(pos int) Query  { b.s.NodePos = pos; return b }
func (b DropMaterializedViewBuilder) setPos(pos int) Query    { b.s.NodePos = pos; return b }
func (b RefreshMaterializedViewBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

func (s CreateMaterializedViewStmt) setPos(pos int) Query  { s.NodePos = pos; return s }
func (s DropMaterializedViewStmt) setPos(pos int) Query    { s.NodePos = pos; return s }
func (s RefreshMaterializedViewStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// Privilege DDL.

func (b GrantBuilder) setPos(pos int) Query  { b.s.NodePos = pos; return b }
func (b RevokeBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

func (s GrantStmt) setPos(pos int) Query  { s.NodePos = pos; return s }
func (s RevokeStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// Rename DDL.

func (s RenameTableStmt) setPos(pos int) Query  { s.NodePos = pos; return s }
func (s RenameColumnStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// Sequence DDL.

func (b CreateSequenceBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
func (b DropSequenceBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }

func (s CreateSequenceStmt) setPos(pos int) Query { s.NodePos = pos; return s }
func (s DropSequenceStmt) setPos(pos int) Query   { s.NodePos = pos; return s }

// Function DDL.

func (b CreateFunctionBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
func (b DropFunctionBuilder) setPos(pos int) Query   { b.s.NodePos = pos; return b }

func (s CreateFunctionStmt) setPos(pos int) Query { s.NodePos = pos; return s }
func (s DropFunctionStmt) setPos(pos int) Query   { s.NodePos = pos; return s }

// Session/administrative statements.

func (s UseStmt) setPos(pos int) Query          { s.NodePos = pos; return s }
func (s LockTablesStmt) setPos(pos int) Query   { s.NodePos = pos; return s }
func (s UnlockTablesStmt) setPos(pos int) Query { s.NodePos = pos; return s }
func (s SetStmt) setPos(pos int) Query          { s.NodePos = pos; return s }
