package sql

// This file collects the SQL function DSL: the generic Func node plus the named
// builders for the functions the SQL standard, PostgreSQL, and MySQL share.
//
// Most builders are mass-produced from a fixed function name with the makeFunc
// helper below, because the function-call (comma) form of the function is
// spelled identically across the three and differs only in name and argument
// count. The handful whose spelling genuinely diverges by
// engine — Extract, Position, StringAgg, the JSON object/access constructors —
// get their own expression node and render per dialect (see compiler.go and
// dialect.go).

// FuncExpression is a function call such as COALESCE(a, b) or SUM(x). With
// Distinct set it renders an aggregate over distinct values, name(DISTINCT
// args...), as in SUM(DISTINCT x); DISTINCT is meaningful only for aggregates.
type FuncExpression struct {
	posField
	Name     string
	Args     []Expression
	Distinct bool
}

func (FuncExpression) SQLExpression() {}
func (FuncExpression) SQLSelection()  {}

// Func creates a function call expression. Non-expression arguments become
// bind values.
func Func(name string, args ...any) FuncExpression {
	expressions := make([]Expression, len(args))
	for i, arg := range args {
		expressions[i] = Expr(arg)
	}
	return FuncExpression{Name: name, Args: expressions}
}

// DistinctFunc creates name(DISTINCT args...), an aggregate over distinct values,
// as in DistinctFunc("SUM", Col("x")) for SUM(DISTINCT x). Non-expression
// arguments become bind values.
func DistinctFunc(name string, args ...any) FuncExpression {
	f := Func(name, args...)
	f.Distinct = true
	return f
}

// Over turns the function into a window expression, e.g.
// RowNumber().Over().PartitionBy(col).OrderBy(Desc(other)).
func (f FuncExpression) Over() WindowExpression { return WindowExpression{Fn: f} }

// makeFunc returns a builder for a fixed-name SQL function: the builder turns
// its arguments into a Func(name, args...) call, so non-expression arguments
// become bind values. It backs the package-level builders below, which differ
// only in name — each builder's doc comment states the argument count it
// expects.
func makeFunc(name string) func(args ...any) FuncExpression {
	return func(args ...any) FuncExpression { return Func(name, args...) }
}

// --- Conditional / NULL handling -------------------------------------------

var (
	// Coalesce creates COALESCE(args...), the first non-NULL argument.
	Coalesce = makeFunc("COALESCE")
	// NullIf creates NULLIF(a, b), NULL when a equals b and a otherwise.
	NullIf = makeFunc("NULLIF")
	// Greatest creates GREATEST(args...), the largest of its arguments.
	Greatest = makeFunc("GREATEST")
	// Least creates LEAST(args...), the smallest of its arguments.
	Least = makeFunc("LEAST")
)

// --- Aggregates ------------------------------------------------------------

var (
	// Sum creates a SUM(expr) aggregate.
	Sum = makeFunc("SUM")
	// Avg creates an AVG(expr) aggregate.
	Avg = makeFunc("AVG")
	// Min creates a MIN(expr) aggregate.
	Min = makeFunc("MIN")
	// Max creates a MAX(expr) aggregate.
	Max = makeFunc("MAX")
	// BitAnd creates a BIT_AND(expr) aggregate, the bitwise AND of all values.
	BitAnd = makeFunc("BIT_AND")
	// BitOr creates a BIT_OR(expr) aggregate, the bitwise OR of all values.
	BitOr = makeFunc("BIT_OR")
	// StdDev creates a STDDEV(expr) aggregate, the sample standard deviation (an
	// alias of StdDevSamp on PostgreSQL and MySQL).
	StdDev = makeFunc("STDDEV")
	// StdDevPop creates a STDDEV_POP(expr) aggregate, the population standard
	// deviation.
	StdDevPop = makeFunc("STDDEV_POP")
	// StdDevSamp creates a STDDEV_SAMP(expr) aggregate, the sample standard
	// deviation.
	StdDevSamp = makeFunc("STDDEV_SAMP")
	// Variance creates a VARIANCE(expr) aggregate, the sample variance (an alias
	// of VarSamp on PostgreSQL and MySQL).
	Variance = makeFunc("VARIANCE")
	// VarPop creates a VAR_POP(expr) aggregate, the population variance.
	VarPop = makeFunc("VAR_POP")
	// VarSamp creates a VAR_SAMP(expr) aggregate, the sample variance.
	VarSamp = makeFunc("VAR_SAMP")
)

// CountExpression represents COUNT(expr), COUNT(*), or COUNT(DISTINCT expr).
type CountExpression struct {
	posField
	Target   Selection
	All      bool
	Distinct bool
}

func (CountExpression) SQLExpression() {}
func (CountExpression) SQLSelection()  {}

// Over turns the aggregate into a window expression.
func (e CountExpression) Over() WindowExpression { return WindowExpression{Fn: e} }

// Count creates a COUNT(selection) aggregate.
func Count(target Selection) CountExpression { return CountExpression{Target: target} }

// CountAll creates a COUNT(*) aggregate.
func CountAll() CountExpression { return CountExpression{All: true} }

// CountDistinct creates a COUNT(DISTINCT selection) aggregate, counting the
// distinct non-NULL values of selection.
func CountDistinct(target Selection) CountExpression {
	return CountExpression{Target: target, Distinct: true}
}

// StringAggExpression concatenates the grouped string values of an aggregate
// into one delimited string. Its spelling diverges by engine — PostgreSQL uses
// the SQL standard STRING_AGG(expr, 'delim') while MySQL uses
// GROUP_CONCAT(expr SEPARATOR 'delim') — so it renders per dialect rather than
// through Func. The delimiter is emitted as a static SQL string literal (MySQL's
// SEPARATOR does not accept a bind placeholder), so it must be a constant.
type StringAggExpression struct {
	posField
	Arg       Expression
	Delimiter string
}

func (StringAggExpression) SQLExpression() {}
func (StringAggExpression) SQLSelection()  {}

// StringAgg creates a STRING_AGG(arg, delimiter) aggregate (GROUP_CONCAT on
// MySQL). A non-expression arg becomes a bind value; delimiter is a constant
// string literal.
func StringAgg(arg any, delimiter string) StringAggExpression {
	return StringAggExpression{Arg: Expr(arg), Delimiter: delimiter}
}

// --- Scalar string functions -----------------------------------------------
//
// These wrap function calls whose comma form is identical in the SQL standard,
// PostgreSQL, and MySQL.

var (
	// Lower creates LOWER(s), the lowercase form of a string.
	Lower = makeFunc("LOWER")
	// Upper creates UPPER(s), the uppercase form of a string.
	Upper = makeFunc("UPPER")
	// Length creates LENGTH(s). Note the engines disagree on the unit: PostgreSQL
	// counts characters while MySQL counts bytes. Use CharLength for a portable
	// character count.
	Length = makeFunc("LENGTH")
	// CharLength creates CHAR_LENGTH(s), the number of characters in a string. It
	// is the portable character count (PostgreSQL and MySQL agree), unlike Length.
	CharLength = makeFunc("CHAR_LENGTH")
	// Trim creates TRIM(s), the string with leading and trailing spaces removed.
	Trim = makeFunc("TRIM")
	// Ltrim creates LTRIM(s), the string with leading spaces removed.
	Ltrim = makeFunc("LTRIM")
	// Rtrim creates RTRIM(s), the string with trailing spaces removed.
	Rtrim = makeFunc("RTRIM")
	// Reverse creates REVERSE(s), the string with its characters reversed.
	Reverse = makeFunc("REVERSE")
	// Md5 creates MD5(s), the hex MD5 hash of a string.
	Md5 = makeFunc("MD5")
	// Ascii creates ASCII(s), the numeric code of the first character of a string.
	Ascii = makeFunc("ASCII")
	// Left creates LEFT(s, n), the leftmost n characters of a string.
	Left = makeFunc("LEFT")
	// Right creates RIGHT(s, n), the rightmost n characters of a string.
	Right = makeFunc("RIGHT")
	// Repeat creates REPEAT(s, n), the string repeated n times.
	Repeat = makeFunc("REPEAT")
	// Replace creates REPLACE(s, from, to), s with every occurrence of from
	// replaced by to.
	Replace = makeFunc("REPLACE")
	// Lpad creates LPAD(s, length, fill), s left-padded with fill to length.
	Lpad = makeFunc("LPAD")
	// Rpad creates RPAD(s, length, fill), s right-padded with fill to length.
	Rpad = makeFunc("RPAD")
	// Substring creates SUBSTRING(s, start[, length]), a substring of s from the
	// 1-based start, as in Substring(Col("name"), 1, 3).
	Substring = makeFunc("SUBSTRING")
	// ConcatWs creates CONCAT_WS(separator, args...), the arguments joined with
	// the separator (NULL arguments are skipped). For plain concatenation that
	// renders portably as PostgreSQL's || operator, prefer Concat.
	ConcatWs = makeFunc("CONCAT_WS")
)

// --- Scalar numeric functions ----------------------------------------------
//
// As with the string functions, the comma form is portable across the SQL
// standard, PostgreSQL, and MySQL.

var (
	// Abs creates ABS(x), the absolute value of a number.
	Abs = makeFunc("ABS")
	// Ceil creates CEIL(x), the smallest integer not less than x.
	Ceil = makeFunc("CEIL")
	// Floor creates FLOOR(x), the largest integer not greater than x.
	Floor = makeFunc("FLOOR")
	// Sqrt creates SQRT(x), the square root of x.
	Sqrt = makeFunc("SQRT")
	// Exp creates EXP(x), e raised to the x power.
	Exp = makeFunc("EXP")
	// Ln creates LN(x), the natural logarithm of x.
	Ln = makeFunc("LN")
	// Sign creates SIGN(x), -1, 0, or 1 according to the sign of x.
	Sign = makeFunc("SIGN")
	// Sin creates SIN(x), the sine of x (radians).
	Sin = makeFunc("SIN")
	// Cos creates COS(x), the cosine of x (radians).
	Cos = makeFunc("COS")
	// Tan creates TAN(x), the tangent of x (radians).
	Tan = makeFunc("TAN")
	// Cot creates COT(x), the cotangent of x (radians).
	Cot = makeFunc("COT")
	// Asin creates ASIN(x), the arc sine of x (radians).
	Asin = makeFunc("ASIN")
	// Acos creates ACOS(x), the arc cosine of x (radians).
	Acos = makeFunc("ACOS")
	// Atan creates ATAN(x), the arc tangent of x (radians).
	Atan = makeFunc("ATAN")
	// Degrees creates DEGREES(x), radians x converted to degrees.
	Degrees = makeFunc("DEGREES")
	// Radians creates RADIANS(x), degrees x converted to radians.
	Radians = makeFunc("RADIANS")
	// Pi creates PI(), the constant pi.
	Pi = makeFunc("PI")
	// Power creates POWER(x, y), x raised to the y power.
	Power = makeFunc("POWER")
	// Atan2 creates ATAN2(y, x), the arc tangent of y/x using the signs of both
	// to pick the quadrant.
	Atan2 = makeFunc("ATAN2")
	// Round creates ROUND(x[, places]), x rounded to the given number of decimal
	// places (zero when omitted).
	Round = makeFunc("ROUND")
)

// --- Date / time functions -------------------------------------------------

// Now creates NOW(), the current date and time (PostgreSQL and MySQL both spell
// it NOW()).
var Now = makeFunc("NOW")

// CurrentDate creates CURRENT_DATE, the current date. It is a niladic keyword
// rather than a function call: the SQL standard and PostgreSQL reject the empty
// parentheses NOW() would render, so it is emitted without them.
func CurrentDate() RawExpression { return Raw("CURRENT_DATE") }

// CurrentTime creates CURRENT_TIME, the current time of day. Like CurrentDate it
// is rendered without parentheses.
func CurrentTime() RawExpression { return Raw("CURRENT_TIME") }

// CurrentTimestamp creates CURRENT_TIMESTAMP, the current date and time. Like
// CurrentDate it is rendered without parentheses.
func CurrentTimestamp() RawExpression { return Raw("CURRENT_TIMESTAMP") }

// ExtractExpression pulls a date/time field (year, month, day, hour, ...) out of
// a date/time operand. It renders as the SQL standard EXTRACT(field FROM
// operand), which PostgreSQL and MySQL both accept. Field is emitted verbatim as
// a keyword (not quoted, not bound), so pass a bare field name such as "year".
type ExtractExpression struct {
	posField
	Field   string
	Operand Expression
}

func (ExtractExpression) SQLExpression() {}
func (ExtractExpression) SQLSelection()  {}

// Extract creates EXTRACT(field FROM operand), e.g.
// Extract("year", Col("created_at")). A non-expression operand becomes a bind
// value.
func Extract(field string, operand any) ExtractExpression {
	return ExtractExpression{Field: field, Operand: Expr(operand)}
}

// PositionExpression finds the 1-based index of the first occurrence of a
// substring within a string, or 0 when it does not occur. It renders as the SQL
// standard POSITION(substring IN string), which PostgreSQL and MySQL both accept
// (the comma forms STRPOS / LOCATE diverge, so this is the portable spelling).
type PositionExpression struct {
	posField
	Substring Expression
	String    Expression
}

func (PositionExpression) SQLExpression() {}
func (PositionExpression) SQLSelection()  {}

// Position creates POSITION(substring IN str). Non-expression operands become
// bind values, as in Position("@", Col("email")).
func Position(substring, str any) PositionExpression {
	return PositionExpression{Substring: Expr(substring), String: Expr(str)}
}

// --- Type conversion -------------------------------------------------------

// CastExpression converts an operand to another SQL type. It renders as the
// standard CAST(operand AS type), which both PostgreSQL and MySQL accept; the
// target type spelling is the caller's responsibility (e.g. "integer",
// "text", "numeric(10, 2)").
type CastExpression struct {
	posField
	Operand Expression
	Type    string
}

func (CastExpression) SQLExpression() {}
func (CastExpression) SQLSelection()  {}

// Cast creates a CAST(operand AS sqlType) expression. A non-expression operand
// becomes a bind value.
func Cast(operand any, sqlType string) CastExpression {
	return CastExpression{Operand: Expr(operand), Type: sqlType}
}

// --- Window / ranking functions --------------------------------------------
//
// Turn any of these into a windowed expression with .Over(); MySQL requires
// version 8.0+ for window functions.

var (
	// RowNumber creates the ROW_NUMBER() window function.
	RowNumber = makeFunc("ROW_NUMBER")
	// Rank creates the RANK() window function.
	Rank = makeFunc("RANK")
	// DenseRank creates the DENSE_RANK() window function.
	DenseRank = makeFunc("DENSE_RANK")
	// PercentRank creates the PERCENT_RANK() window function, the relative rank in
	// the range [0, 1].
	PercentRank = makeFunc("PERCENT_RANK")
	// CumeDist creates the CUME_DIST() window function, the cumulative
	// distribution in the range (0, 1].
	CumeDist = makeFunc("CUME_DIST")
	// Ntile creates the NTILE(buckets) window function, which distributes the
	// partition's rows into the given number of ranked buckets.
	Ntile = makeFunc("NTILE")
	// Lag creates the LAG(expr) window function, the value from a preceding row.
	Lag = makeFunc("LAG")
	// Lead creates the LEAD(expr) window function, the value from a following row.
	Lead = makeFunc("LEAD")
	// FirstValue creates the FIRST_VALUE(expr) window function, the value from the
	// first row of the window frame.
	FirstValue = makeFunc("FIRST_VALUE")
	// LastValue creates the LAST_VALUE(expr) window function, the value from the
	// last row of the window frame.
	LastValue = makeFunc("LAST_VALUE")
	// NthValue creates the NTH_VALUE(expr, n) window function, the value from the
	// nth row of the window frame.
	NthValue = makeFunc("NTH_VALUE")
)

// --- JSON ------------------------------------------------------------------

// JSONArray creates a JSON_ARRAY(args...) expression that builds a JSON array
// from its arguments. The name is SQL/JSON standard (PostgreSQL 16+, MySQL
// 5.7+); on older PostgreSQL use Func("json_build_array", ...).
var JSONArray = makeFunc("JSON_ARRAY")

// JSONArrayAgg creates a JSON_ARRAYAGG(expr) aggregate that collects the grouped
// values into a JSON array (PostgreSQL 16+, MySQL 5.7.22+; older PostgreSQL has
// json_agg / jsonb_agg via Func).
var JSONArrayAgg = makeFunc("JSON_ARRAYAGG")

// JSONObjectExpression builds a JSON object from key/value pairs. Its argument
// syntax diverges by engine — MySQL takes a flat JSON_OBJECT(key, value, ...)
// list, while the SQL/JSON standard (PostgreSQL 16+) requires a key VALUE value
// separator — so it renders per dialect rather than through Func. Agg selects
// the JSON_OBJECTAGG aggregate, which takes a single key/value pair.
type JSONObjectExpression struct {
	posField
	Pairs []Expression
	Agg   bool
}

func (JSONObjectExpression) SQLExpression() {}
func (JSONObjectExpression) SQLSelection()  {}

// JSONObject creates a JSON_OBJECT expression. Arguments alternate keys and
// values: JSONObject("id", col, "name", other). Non-expression arguments become
// bind values.
func JSONObject(args ...any) JSONObjectExpression {
	pairs := make([]Expression, len(args))
	for i, arg := range args {
		pairs[i] = Expr(arg)
	}
	return JSONObjectExpression{Pairs: pairs}
}

// JSONObjectAgg creates a JSON_OBJECTAGG(key, value) aggregate that collects the
// grouped key/value pairs into a JSON object.
func JSONObjectAgg(key, value any) JSONObjectExpression {
	return JSONObjectExpression{Pairs: []Expression{Expr(key), Expr(value)}, Agg: true}
}

// JSONAccessExpression extracts the value at a path from a JSON operand. Path
// steps are object keys (string) or array indices (int) and are rendered as
// static path literals, not bind parameters. AsText selects the text-returning
// form. The path is portable: each dialect (a JSONAccessor) renders it in its
// native syntax (PostgreSQL -> / ->>, MySQL JSON_EXTRACT / JSON_UNQUOTE).
type JSONAccessExpression struct {
	posField
	Operand Expression
	Path    []any
	AsText  bool
}

func (JSONAccessExpression) SQLExpression() {}
func (JSONAccessExpression) SQLSelection()  {}

// JSONGet extracts the JSON value at path from a JSON operand, e.g.
// JSONGet(Col("data"), "address", "city") or JSONGet(Col("tags"), 0). Path
// steps are object keys (string) or array indices (int).
func JSONGet(operand any, path ...any) JSONAccessExpression {
	return JSONAccessExpression{Operand: Expr(operand), Path: path}
}

// JSONGetText is JSONGet returning the extracted value as text (PostgreSQL ->>,
// MySQL JSON_UNQUOTE(JSON_EXTRACT(...))), so it compares against string values.
func JSONGetText(operand any, path ...any) JSONAccessExpression {
	return JSONAccessExpression{Operand: Expr(operand), Path: path, AsText: true}
}
