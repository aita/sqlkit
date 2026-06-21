package roundtrip_test

// Semantic-equivalence oracle.
//
// The L1/L2 round-trip oracle (roundtrip_test.go) checks that the compiler's
// output is valid and text-stable: parse(stmt)=q1 -> render=text1 ->
// parse(text1)=q2 -> render=text2, asserting text1==text2. Its blind spot is a
// render that is valid AND text-fixpoint but changes MEANING. Such a bug shows
// up only as a STRUCTURAL difference between q1 and q2 (the parsed-then-rendered
// query), because a meaning-changing render makes the second parse build a
// different AST than the first.
//
// This file compares q1 and q2 structurally, ignoring source positions. A
// position is metadata the compiler discards, so two queries that name the same
// thing but were parsed from different offsets must compare equal; only a real
// shape difference (a different object, a reassociated tree) is a finding.
//
// Equality is reflect.DeepEqual over a position-zeroed copy of each query, with
// a small set of benign-normalization rules applied first (see normalize). Those
// rules absorb the differences a parser legitimately introduces when it re-reads
// the compiler's canonical output (e.g. the compiler always emits an explicit
// alias the source omitted), which are not meaning changes.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unsafe"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
)

// TestSemanticEquivalence asserts, for every corpus statement that parses to a
// typed (non-raw) query, that re-parsing the compiler's rendering yields a
// structurally identical query modulo source positions and benign
// normalizations. A structural difference is a candidate silent meaning change.
func TestSemanticEquivalence(t *testing.T) {
	for _, fe := range frontends() {
		fe := fe
		t.Run(fe.name, func(t *testing.T) {
			for _, dir := range fe.dirs {
				for _, c := range loadCorpus(t, dir) {
					c := c
					t.Run(c.id, func(t *testing.T) {
						semanticCheck(t, fe, c.stmt)
					})
				}
			}
		})
	}
}

// semanticCheck runs one parse/render/parse cycle and compares q1 to q2
// position-insensitively. Raw passthroughs are skipped: their meaning is the
// backing parser's verbatim text, not a structure sqlkit owns.
func semanticCheck(t *testing.T, fe frontend, stmt string) {
	t.Helper()

	q1, err := sqlparse.ParseOne(fe.parser, stmt)
	if err != nil {
		t.Fatalf("parse failed for corpus statement: %v\n  stmt: %s", err, stmt)
	}
	if isRaw(q1) {
		return
	}

	text1, _, err := q1.ToSQL(fe.dialect)
	if err != nil {
		t.Fatalf("ToSQL errored on a parseable statement: %v\n  stmt: %s", err, stmt)
	}

	q2, err := sqlparse.ParseOne(fe.parser, text1)
	if err != nil {
		t.Fatalf("compiler emitted SQL its own grammar rejects: %v\n  generated: %s\n  from: %s", err, text1, stmt)
	}
	if isRaw(q2) {
		// Already reported by the L1 oracle; not a structural-equivalence concern.
		return
	}

	a := canonical(q1)
	b := canonical(q2)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("semantic drift: q1 and q2 differ structurally (positions ignored)\n  stmt:      %s\n  rendered:  %s\n  q1: %#v\n  q2: %#v",
			stmt, text1, a, b)
	}
}

// canonical returns a position-zeroed, benign-normalized copy of q suitable for
// reflect.DeepEqual. It deep-copies q (so the original AST is untouched), zeros
// every NodePos field in the copy, then applies the normalization rules.
func canonical(q sql.Query) sql.Query {
	// Mirror q into an addressable interface value first: reflect.ValueOf(q) is
	// not addressable, so deepCopyInto could not reach its unexported fields
	// through unsafe.UnsafeAddr. An addressable copy of the interface gives every
	// nested value a real address.
	src := reflect.New(reflect.TypeOf(q)).Elem()
	src.Set(reflect.ValueOf(q))
	v := deepCopyValue(src)
	zeroPositions(v)
	normalize(v)
	return v.Interface().(sql.Query)
}

// zeroPositions walks v and sets every int field named "NodePos" to 0, the value
// an unparsed (builder-built) node carries. It descends through pointers,
// slices, arrays, maps, interfaces, and struct fields, reaching unexported
// fields through unsafe so the whole tree is normalized, not just its exported
// surface. v must be addressable (deepCopyValue produces addressable values).
func zeroPositions(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			zeroPositions(v.Elem())
		}
	case reflect.Interface:
		if !v.IsNil() {
			mutateInterface(v, zeroPositions)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			zeroPositions(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			// Map values are not addressable; copy, normalize, write back.
			ev := v.MapIndex(k)
			cp := reflect.New(ev.Type()).Elem()
			cp.Set(ev)
			zeroPositions(cp)
			v.SetMapIndex(k, cp)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if t.Field(i).Name == "NodePos" && f.Kind() == reflect.Int {
				setInt(f, 0)
				continue
			}
			zeroPositions(f)
		}
	}
}

// paramSentinel is the single canonical form every bound-parameter expression
// (a literal ValueExpression, or the placeholder RawExpression it re-parses to)
// is collapsed to before comparison.
var paramSentinel = sql.RawExpression("\x00param\x00")

// placeholderRE matches a re-parsed bind placeholder in any dialect spelling the
// frontends produce: ?, $1, :name. A RawExpression holding one of these is the
// parser's reading of a compiler-emitted placeholder, not a static fragment.
var placeholderRE = regexp.MustCompile(`^(\?|\$[0-9]+|:[A-Za-z_][A-Za-z0-9_]*)$`)

// isParamExpr reports whether x is a bound-parameter expression in either of its
// two round-trip forms: the literal ValueExpression the first parse builds, or
// the placeholder RawExpression the second parse builds from the rendered
// placeholder.
func isParamExpr(x any) bool {
	switch e := x.(type) {
	case sql.ValueExpression:
		return true
	case sql.RawExpression:
		return placeholderRE.MatchString(string(e))
	}
	return false
}

// mutateInterface runs fn over the dynamic value held in the addressable
// interface value v, then writes the mutated value back into v. An interface's
// element is not addressable, so it is mirrored into an addressable buffer
// first; without the write-back, in-place mutations to that buffer would be
// lost.
func mutateInterface(v reflect.Value, fn func(reflect.Value)) {
	concrete := reflect.ValueOf(exported(v).Interface())
	addr := reflect.New(concrete.Type()).Elem()
	addr.Set(concrete)
	fn(addr)
	settable(v).Set(addr)
}

// normalize applies the benign-normalization rules to an already position-zeroed
// copy. Each rule rewrites a parser-introduced cosmetic difference into a single
// canonical form so reflect.DeepEqual does not flag it. See the package-level
// comment for the catalogue.
func normalize(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			normalize(v.Elem())
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		// Parameterization: the compiler renders every literal ValueExpression as a
		// bind placeholder ($1, ?), which re-parses as a placeholder RawExpression.
		// Both denote "a bound parameter here"; the literal's value moves out of
		// band into the args list, so this is meaning-preserving, not a semantic
		// change. Collapse both forms to one sentinel before comparing. (A
		// non-placeholder RawExpression — a genuine static fragment — is left
		// untouched, so a real raw/typed divergence still shows.)
		if isParamExpr(exported(v).Interface()) {
			settable(v).Set(reflect.ValueOf(sql.Expression(paramSentinel)))
			return
		}
		mutateInterface(v, normalize)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			normalize(v.Index(i))
		}
	case reflect.Struct:
		// Fold identifier case: unquoted identifiers are case-insensitive, and the
		// two frontends differ in how they case-normalize on re-parse. Folding the
		// Name of every Ident removes that purely-cosmetic difference. (A real
		// object-identity change — e.g. one name becoming two — survives folding.)
		if v.Type() == reflect.TypeOf(sql.Ident{}) {
			nameField := v.FieldByName("Name")
			settable(nameField).SetString(strings.ToLower(strings.TrimSpace(exported(nameField).String())))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			normalize(v.Field(i))
		}
	}
}

// deepCopyValue returns a fully addressable deep copy of v, so the caller may
// mutate the copy (zero positions, normalize) without touching the source AST.
// It reaches unexported fields through unsafe, mirroring zeroPositions.
func deepCopyValue(v reflect.Value) reflect.Value {
	out := reflect.New(v.Type()).Elem()
	deepCopyInto(out, v)
	return out
}

func deepCopyInto(dst, src reflect.Value) {
	switch src.Kind() {
	case reflect.Ptr:
		if src.IsNil() {
			return
		}
		np := reflect.New(src.Type().Elem())
		deepCopyInto(np.Elem(), src.Elem())
		setPtr(dst, np)
	case reflect.Interface:
		if src.IsNil() {
			return
		}
		// An interface's dynamic value is never addressable and carries a read-only
		// flag when the interface field itself is unexported. Round-trip it through
		// Interface() (clearing the flag — src is addressable, so exported(src)
		// succeeds) into a fresh, addressable mirror the recursion can descend.
		concrete := reflect.ValueOf(exported(src).Interface())
		addr := reflect.New(concrete.Type()).Elem()
		addr.Set(concrete)
		cp := reflect.New(concrete.Type()).Elem()
		deepCopyInto(cp, addr)
		setValue(dst, cp)
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		ns := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			deepCopyInto(ns.Index(i), src.Index(i))
		}
		setValue(dst, ns)
	case reflect.Array:
		for i := 0; i < src.Len(); i++ {
			deepCopyInto(dst.Index(i), src.Index(i))
		}
	case reflect.Map:
		if src.IsNil() {
			return
		}
		nm := reflect.MakeMapWithSize(src.Type(), src.Len())
		for _, k := range src.MapKeys() {
			ev := reflect.New(src.Type().Elem()).Elem()
			deepCopyInto(ev, src.MapIndex(k))
			nm.SetMapIndex(k, ev)
		}
		setValue(dst, nm)
	case reflect.Struct:
		for i := 0; i < src.NumField(); i++ {
			deepCopyInto(dst.Field(i), src.Field(i))
		}
	default:
		setValue(dst, src)
	}
}

// The set* helpers write through dst even when it is an unexported struct field,
// which reflect forbids via the ordinary Set path. They rebind the field to an
// addressable, settable alias of the same memory using unsafe.Pointer.

func settable(v reflect.Value) reflect.Value {
	if v.CanSet() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

// exported re-binds v through unsafe so its read-only (unexported) flag is
// cleared, making it usable as the argument to Set. v must be addressable.
func exported(v reflect.Value) reflect.Value {
	if v.CanInterface() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

func setValue(dst, src reflect.Value) { settable(dst).Set(exported(src)) }
func setPtr(dst, np reflect.Value)    { settable(dst).Set(np) }
func setInt(dst reflect.Value, n int64) {
	settable(dst).SetInt(n)
}

// TestSemanticOracleIntegrity guards the oracle itself: it must distinguish the
// known silent-meaning-change shape (one quoted dotted name "x.y" naming a table
// vs schema "x" + table "y"), and must not over-collapse the parameterization
// normalization so far that a literal operand and a column operand compare equal.
// If these regress, the oracle has gone blind and TestSemanticEquivalence's
// green is meaningless.
func TestSemanticOracleIntegrity(t *testing.T) {
	dottedName := sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("x.y")}).Stmt()
	schemaTable := sql.Select(sql.Star()).From(sql.Table{Schema: sql.NewIdent("x"), Name: sql.NewIdent("y")}).Stmt()
	if reflect.DeepEqual(canonical(dottedName), canonical(schemaTable)) {
		t.Error("oracle blind: Table{Name:\"x.y\"} compares equal to Table{Schema:\"x\",Name:\"y\"}")
	}

	litOperand := sql.Select(sql.Star()).Where(sql.Eq("a", sql.Val(1))).Stmt()
	colOperand := sql.Select(sql.Star()).Where(sql.Eq("a", sql.Col("b"))).Stmt()
	if reflect.DeepEqual(canonical(litOperand), canonical(colOperand)) {
		t.Error("oracle over-collapsed: a literal operand compares equal to a column operand")
	}
}

// FuzzSemanticEquivalence mutates outward from the on-disk corpus, asserting the
// position-insensitive structural equivalence of q1 and q2 for every typed query
// a frontend accepts. It is the semantic counterpart of FuzzRoundTrip: the
// fixpoint fuzzer catches text instability, this one catches a render that is
// text-stable yet changes meaning (q1 and q2 differ in shape).
func FuzzSemanticEquivalence(f *testing.F) {
	for _, stmt := range seedStatements(f) {
		f.Add(stmt)
	}

	f.Fuzz(func(t *testing.T, stmt string) {
		for _, fe := range frontends() {
			q1, err := sqlparse.ParseOne(fe.parser, stmt)
			if err != nil || isRaw(q1) {
				continue
			}
			text1, _, err := q1.ToSQL(fe.dialect)
			if err != nil {
				// Validity is FuzzRoundTrip's contract; not asserted here.
				continue
			}
			q2, err := sqlparse.ParseOne(fe.parser, text1)
			if err != nil || isRaw(q2) {
				continue
			}
			a := canonical(q1)
			b := canonical(q2)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("[%s] semantic drift (positions ignored)\n  stmt:     %s\n  rendered: %s\n  q1: %#v\n  q2: %#v",
					fe.name, stmt, text1, a, b)
			}
		}
	})
}
