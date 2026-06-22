---
name: bug-hunt
description: >-
  Systematically hunt for correctness bugs in the sqlkit codebase (compiler,
  parser frontends, redaction, analysis, migrate, codegen, query builder,
  routing, paginator). Use when asked to "find bugs", "hunt for bugs", do a
  "bug hunt", "hardening pass", "robustness pass", or "another round" of bug
  finding, or to harden a specific package against panics, invalid SQL,
  silent meaning changes, non-determinism, or PII leaks. Encodes the
  oracle + fuzz + differential + coverage-probe methodology this repo's
  past bug-hunt PRs (#127, #130, #131, #140, …) have used.
---

# Bug hunting in sqlkit

This repo finds bugs with **oracles, fuzzing, differential testing, coverage
probing, and fault injection** — not by reading code and guessing. A bug hunt
is an empirical search: build a check that *must* hold, run it broadly over real
and mutated inputs, and let it surface the violations. Every past round
(`#127` ~30 bugs, `#130`, `#131` round 2, `#140` the per-bug split) worked this
way. Reproduce that process; don't improvise a weaker one.

## The governing principle

Most of sqlkit's surface is "text in → AST → text out" or "Go in → AST → text
out". The single invariant the whole kit is held to:

> **Any statement the backing parser (go-pgquery / TiDB) accepts must yield a
> usable `Query` (typed, or a raw passthrough) and must never panic, emit
> invalid SQL, or silently change meaning.** For security-sensitive passes
> (redaction), a sensitive literal must never survive masking (no fail-open).

Almost every bug found here is a concrete violation of that sentence. When you
inspect a candidate, classify it against the principle: *panic? invalid SQL?
silent meaning change? non-determinism? PII leak?* If it is none of those, it is
probably not a bug — see "What is NOT a bug".

## Workflow

1. **Pick a target surface** and read its existing tests first — they tell you
   what is already guaranteed and what the prior rounds already fixed (grep the
   git log: `git log --oneline --grep=fix`).
2. **Run several explorations in parallel** (see Techniques). `#131` ran four at
   once: semantic oracle, builder-first property testing, low-coverage branch
   probing, and a real-engine differential check. Use the `Agent` tool to fan
   out independent explorations.
3. **For each violation found**: write the smallest reproducer, confirm it
   breaks the invariant, then fix it.
4. **Add a permanent regression test** — a unit test in the file matching the
   code under test, and keep any fuzzer-found input as a corpus/seed entry.
5. **Validate the whole suite** (see `validation.md`) before claiming a fix.
6. **One fix per commit/PR.** `#140` was explicitly split into ten per-bug PRs
   (`#141`–`#150`) for independent review. Do the same: small, self-contained,
   each with its own test.

## Techniques (run these, in parallel where possible)

### 1. Oracles — properties that must hold over a whole corpus + as fuzz targets
The strongest tools here. Existing oracles live in `internal/tests/roundtrip/`:

- **Round-trip (L1/L2)** `FuzzRoundTrip` / `roundtrip_test.go`: parse → `ToSQL`
  must (L1) re-parse and (L2) be a rendering fixpoint, for every dialect. Catches
  invalid emitted SQL and unstable rendering.
- **Semantic-equivalence** `FuzzSemanticEquivalence` / `semantic_test.go`:
  `q1` and the re-parsed `q2` must be structurally equal (positions ignored).
  Catches *silent meaning changes* the text-only oracle is blind to (e.g.
  `"x.y"` one object vs `"x"."y"` qualified).
- **Precedence** (`sqlparse/*` `precedence_test.go` style): asserts the exact
  parenthesization the compiler emits, catching a wrong parse tree.
- **Redaction no-leak** `FuzzRedactNoLeak`, `FuzzRedactParsedNoLeak`,
  `FuzzRedactRobust`, `FuzzRedactRawParser`: a sensitive literal must not appear
  in the masked output.

When you find a class of bug not covered by an oracle, **add a new oracle** — that
is how `#131` added the semantic one. Guard any benign normalizations a new
oracle makes with an integrity meta-test so it can't silently widen into
blindness.

### 2. Fuzzing — drive the oracles over mutated inputs
The seed corpus is real SQL in `internal/tests/roundtrip/testdata/{common,postgres,mysql}`.
Run targets long enough to matter (`#131`: ~240s, ~2.2M execs, clean). See
`validation.md` for the full target list and commands. Every fuzzer-found
failure becomes a permanent regression seed.

### 3. Differential testing — two independent implementations must agree
- **Dialect differential**: render the same construct for PostgreSQL and MySQL;
  divergence that isn't an intended dialect difference is a bug.
- **Real-engine differential**: feed corpus statements to a live PostgreSQL /
  MySQL (the `internal/tests/integration` suite, or a local server) and check the
  real engine accepts what sqlkit emits. `#131` ran 181/181 corpus statements
  against a real PG server.

### 4. Coverage-driven branch probing
Generate coverage, then read the **uncovered branches** in the converters and
compilers — low-coverage paths are where silent type/attribute drops hide (e.g.
`#131`'s dropped `COLLATE`, field-qualified `INTERVAL`, internal `"char"`). Probe
each uncovered branch with a targeted input and check the invariant.

### 5. Builder-first vs parser-first
The parser-first fuzzer under-exercises the compiler/builder side. Drive queries
through the **fluent builder API** directly (the production path) to reach
branches no parsed corpus can — this is where `#131`'s unary-`+/-`,
single-element `ROW`, and `OFFSET`-without-`LIMIT` bugs came from.

### 6. Fault injection — error / panic / cancel paths
Pattern in `fault_test.go`: inject a driver error, a cancelled context / exceeded
deadline, or a panicking callback, and assert "no panic escapes, errors
propagate (reachable via `errors.Is`/`errors.As`), cursors close, transactions
roll back". Use the `internal/testutil` fake driver's counters.

### 7. Determinism
Anything that iterates a Go `map` and emits the result in order is a suspect:
preload IN-value order, migrate FK/index diff order, codegen output. The fix is
to sort by a stable key; the test asserts a fixed expected order
(`#142`, `#147`).

## Common bug classes seen in this repo (use as a checklist)

- **Parser → raw fallback gaps**: a construct sqlkit can't model that renders
  *wrong* instead of falling back to a raw passthrough — dotted/quoted
  identifiers re-split into qualified names (`"a.b"` → `"a"."b"`, a different
  object), non-bare function/table names, empty `ROW()`, count-mismatched
  `INSERT`. Fix = fall back to raw, keeping the original text.
- **Panics on untrusted input**: unchecked type assertions in fold/negate paths,
  parser panics on over-long literals. Fix = guard the assertion / recover.
- **Invalid dialect SQL**: MySQL `ROW(x)` (needs ≥2), `OFFSET` without `LIMIT`,
  `NULLS` ordering, unterminated backslash strings. Fix = reject at compile time
  or emit the dialect-correct form.
- **Silent type/attribute drops** in DDL/type converters: `COLLATE`,
  `CHARACTER SET`, `ZEROFILL`, interval field qualifiers, internal `"char"`.
- **Non-determinism** from map iteration (above).
- **Fail-open PII leaks** in redaction: a sensitive literal wrapped in an
  expression, behind a CTE/subquery/alias, that escapes masking.
- **Fingerprint/analysis**: arity sensitivity (`IN (1,2)` vs `IN (1,2,3)`),
  un-collapsed placeholders/`LIMIT`/`OFFSET`, doubled-quote escapes.
- **Codegen**: invalid/duplicate Go identifiers, double pointers, unescaped
  struct-tag content.

## What is NOT a bug (don't file these)

- A construct sqlkit doesn't model that **correctly** falls back to a raw
  passthrough (raw is the designed escape hatch; its re-parse/fixpoint is the
  backing parser's concern, not sqlkit's).
- A documented engine gap rejected **at compile time with a clear error** (e.g.
  MySQL has no `RETURNING`) — see `docs/limitations.md`. That is the contract,
  not a bug.
- A coverage number being low. Don't grow the round-trip corpus to chase
  coverage — it has a structural ceiling (see `internal/tests/README.md`). Add a
  builder unit test or a fuzz seed instead.

## Output

Report findings as a short list, each with: the violated invariant, a minimal
reproducer, the root cause, and the fix. Then implement them as **separate,
small commits/PRs, one bug each, each with its regression test**, and only after
the full validation suite (`validation.md`) is green. If a hunt finds no live
bugs, say so plainly and leave behind any new oracle as a permanent guard (a
clean hunt is a valid result — `#118` found none).
