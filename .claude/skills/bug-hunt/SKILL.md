---
name: bug-hunt
description: >-
  Systematically hunt for correctness bugs in the sqlkit codebase — both the
  text-in/AST/text-out surfaces (compiler, parser frontends, redaction,
  analysis, fingerprint) and the stateful/runtime layers (query builder,
  migrate, codegen, routing, paginator, meta/decl schema & checksums, uow,
  querytrace/driver, sqltest). Use when asked to "find bugs", "hunt for bugs",
  do a "bug hunt", "hardening pass", "robustness pass", or "another round" of
  bug finding, or to harden a specific package against panics, invalid SQL,
  silent meaning changes, non-determinism, structural-identity collisions,
  write-path asymmetry, or PII leaks. Encodes the oracle + fuzz + differential
  + coverage-probe + audit methodology this repo's past bug-hunt PRs
  (#127, #130, #131, #140, #141–#166, …) have used.
---

# Bug hunting in sqlkit

This repo finds bugs with **oracles, fuzzing, differential testing, coverage
probing, fault injection, and focused code-reading audits**. A bug hunt is an
empirical search: build a check that *must* hold (or, where no oracle fits,
reason precisely about one contract), then confirm the violation with a
reproducer. Every past round (`#127` ~30 bugs, `#130`, `#131` round 2, `#140`
the per-bug split, `#141`–`#166` the latest) worked this way. Reproduce that
process; don't improvise a weaker one.

## Match the method to the surface

The strongest method depends on what layer you are probing. Pick the target
surface first, then the matching method:

- **Text-in → AST → text-out** (`sql` compiler, `sqlparse/*`, `redaction`,
  `analysis`/fingerprint): **oracles + fuzzing + differential** (Techniques 1–5).
  These are property-checkable over a corpus, so let the checks find the bugs —
  don't hand-guess.
- **Stateful / data / wrapper layers** (`meta`/`decl`, `uow`, `querytrace/driver`,
  `sqltest`, `migrate` runtime, `routing`/`exec`): no text oracle covers these.
  Hunt them with a **focused code-reading audit + a targeted DB-driven (or
  fake-driver) regression test**, plus the structural oracles in Technique 9.
  This is where the `#152`, `#154`–`#157` bugs lived.

## The governing principle

Most of sqlkit's surface is "text in → AST → text out" or "Go in → AST → text
out". The single invariant the whole kit is held to:

> **Any statement the backing parser (go-pgquery / TiDB) accepts must yield a
> usable `Query` (typed, or a raw passthrough) and must never panic, emit
> invalid SQL, or silently change meaning.** For security-sensitive passes
> (redaction), a sensitive literal must never survive masking (no fail-open).

Almost every text-surface bug is a concrete violation of that sentence. When you
inspect a candidate, classify it: *panic? invalid SQL? silent meaning change?
non-determinism? PII leak?* The stateful layers add their own contracts —
*structural-identity collision (a checksum/Equal that omits a field)? write-path
asymmetry (INSERT vs UPDATE encoding)? FK-unsafe ordering? a dropped or
over-advertised driver interface? a metric counted wrong?* If a candidate fits
none of these, it is probably not a bug — see "What is NOT a bug".

## Workflow

1. **Pick a target surface** and read its existing tests first — they tell you
   what is already guaranteed and what the prior rounds already fixed (grep the
   git log: `git log --oneline --grep=fix`).
2. **Run several explorations in parallel** (see Techniques). `#131` ran four at
   once: semantic oracle, builder-first property testing, low-coverage branch
   probing, and a real-engine differential check. Use the `Agent` tool to fan
   out independent explorations, each over a **scoped surface** (one package or
   technique) so they don't overlap. **Re-verify every finding yourself before
   acting** — agent output runs a high false-positive rate; treat each candidate
   as unconfirmed until you reproduce it (see "Triage: confirm before you fix").
3. **For each violation found**: write the smallest reproducer, confirm it
   breaks the invariant, then fix it.
4. **Add a permanent regression test, and prove it catches the bug** — a unit
   test in the file matching the code under test, and keep any fuzzer-found input
   as a corpus/seed entry. Then **confirm the test fails without the production
   change** (revert only the prod file in a throwaway worktree and re-run — see
   `validation.md`). A test that passes both with and without the fix guards
   nothing — this is what exposed a determinism fix shipped with *no* test (#147)
   and a determinism test that false-passed ~74% of the time against the bug
   (#142).
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
to sort by a stable key. The test must **repeat the operation** (≈64–200×, or use
enough distinct keys that an accidental match is negligible) and assert a fixed
expected order — a single assertion can pass by chance under randomized map
iteration (`#142`'s one-shot test false-passed ~74% of the time against the bug;
`#147`).

### 8. Canonical-encoding completeness
A checksum, fingerprint, `Equal` method, or cache key must encode *every*
structurally significant field; an omitted field is a silent collision (distinct
things compare equal) or undetected drift. Enumerate the struct's fields and
write a per-field-mutation property test that changes each one and asserts the
digest changes — `meta`'s `TestKeyDiffersOnStructuralChange` is the template. One
identity function alone yielded two bugs this way: omitted FK `ON DELETE`/`ON
UPDATE` actions (#151) and the column array flag (#166).

### 9. Stateful / data / wrapper-layer oracles
The text oracles don't reach `meta`/`decl`, `uow`, `querytrace/driver`, or
`sqltest`. These layers still have checkable round-trips and parities — use them
as the oracle, backed by the integration suite or the fake driver:

- **Binding ↔ catalog checksum reconciliation**: a generated binding's checksum
  must equal the checksum of the catalog rebuilt from the schema. The FK-action
  omission (#151) surfaced exactly here, via `internal/tests/integration`'s
  uow binding-vs-catalog check.
- **DDL generate → introspect round-trip**: run `CreateSQL` on a live engine,
  introspect it back into `meta`, and assert the result is checksum-equal to the
  source catalog. Catches DDL the generator omits (object-only schemas that never
  got a `CREATE SCHEMA`, #157).
- **INSERT/UPDATE parity**: the same column value must reach the driver
  identically through the Insert builder and the `uow` UPDATE path. A value
  JSON-encoded on INSERT but passed raw on UPDATE (#155) breaks this.
- **Wrapper-fidelity differential**: drive a type through the wrapped vs unwrapped
  `database/sql` driver and assert the wrapper advertises *exactly* the optional
  driver interfaces (`SessionResetter`, `Validator`, `NamedValueChecker`,
  `ColumnConverter`, …) the underlying one implements — no dropping, no
  over-advertising (the `querytrace/driver` `conn_test.go`/`stmt_test.go` pattern).

## Triage: confirm before you fix

Most candidates — especially from parallel agents — are false positives (these
hunts run a high dismissal rate). Before writing a fix, clear two bars:

1. **Prove it is genuinely broken on the real contract.** Reproduce the
   violation, and check the construct is actually invalid — a cast or surrounding
   context can rescue what looks wrong. Empty `ARRAY[]` *looks* invalid but
   `ARRAY[]::int[]` is valid PostgreSQL, so rejecting empty arrays would break a
   real use; empty `GROUPING SETS ()` has no such rescue and *was* a bug (#165).
2. **Prove the fix doesn't regress the valid common case.** A plausible fix can
   be net-negative: adding `float`/`double` to the introspection decimal set
   would turn a plain `FLOAT` into a bogus `FLOAT(12,0)`, so it was dismissed, not
   shipped. If the fix changes behavior for inputs that were already correct, it
   is the wrong fix.

A candidate that fails either bar is not a fix to make. Record the dismissal with
its reasoning — a well-reasoned non-bug is a valid hunt outcome.

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
- **Incomplete canonical encodings**: a checksum / fingerprint / `Equal` / cache
  key that omits a structurally significant field, so distinct structures collide
  or drift goes undetected (`#151` FK actions, `#166` array flag). Fix = fold the
  field into the encoding, ideally append-only so unchanged inputs keep their
  digest (see `validation.md`).
- **DDL generation coverage gaps**: an object-only schema (enum / sequence /
  matview, no tables) never emits its `CREATE SCHEMA` even though its DDL is
  schema-qualified (#157).
- **Write-path asymmetry**: a value encoded one way on INSERT but another on
  UPDATE — e.g. a json-tagged column JSON-encoded by the Insert builder but passed
  as a raw map/struct to the driver on the `uow` UPDATE path (#155).
- **Wrapper optional-interface hiding / over-advertising**: a `database/sql`
  wrapper that *drops* an optional interface the inner driver implements
  (`SessionResetter`, `Validator`), or *unconditionally advertises* one
  (`NamedValueChecker`) and thereby suppresses the inner `ColumnConverter`.
- **Cross-table ordering vs constraints**: an FK-unsafe apply order — inserting a
  child before its parent, or deleting a parent before its child — in `uow` flush
  or migration apply (#156).
- **Metric-by-subtraction**: a count derived as `A − B` instead of by direct
  classification, so a third category is miscounted — `sqltest` deriving
  `Reads = QueryCount − WriteCount` counted DDL as a read.
- **Post-hoc text rewriting of rendered SQL**: a `strings.Replace` over built SQL
  that can corrupt embedded user data — REVOKE rewriting the first `" TO "`, which
  also hits a quoted identifier containing `" TO "` (#154). Fix = build the variant
  from the AST, not by editing the string.
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

## Severity: how bad is the bug

Rate each finding by **impact if it persists**, separately from how risky the
*fix* is to land (that's Mergeability — the two are orthogonal). Severity drives
priority and how loudly to flag it.

- **Critical — silent wrong data, or a security breach.** Wrong rows returned or
  written with no error, or sensitive data exposed — the caller cannot tell
  anything went wrong. Examples: a silent meaning change that alters the result
  set (GROUP_CONCAT separator dropped → wrong aggregate; quoted `"x.y"` naming a
  different object), a fail-open PII leak in redaction, a structural-identity
  collision (checksum / `Equal` omission) that serves a stale or wrong
  cached/deduped result, write-path asymmetry sending the wrong value to the
  driver, FK-unsafe ordering corrupting a write set.
- **High — crash, or a lost integrity guarantee.** A panic on accepted input
  (DoS on untrusted SQL), a dropped transaction/atomicity guarantee, or an
  injection-relevant escaping gap. Detectable, but breaks availability or a
  safety guarantee.
- **Medium — loud, contained failure.** Emits SQL the engine rejects, or
  hard-errors where a valid construct should work, or non-determinism that only
  causes flaky diffs / output churn (migrate diff order, codegen). The operation
  fails visibly; no silent corruption, no crash. Most "invalid dialect SQL" bugs
  land here.
- **Low — no production-visible wrong behavior.** Non-fixpoint rendering,
  internal-only inconsistency, robustness hardening, cosmetic. Worth fixing to
  keep an oracle honest; not urgent.

Weight by **reachability**: the same impact is more urgent on a common path (any
`SELECT`) than behind an exotic construct only a hand-written query reaches.
State both — e.g. "Critical impact, low reachability (schema-qualified operators
only)". Lead with severity for triage; lead with Mergeability for landing.

## Output

Report findings as a short list, each with: its **severity (and reachability)**,
the violated invariant, a minimal reproducer, the root cause, and the fix. Then
implement them as **separate, small commits/PRs, one bug each, each with its
regression test**, and only after the full validation suite (`validation.md`) is
green. Each fix PR also declares its Mergeability verdict (above). If a hunt
finds no live bugs, say so plainly and leave behind any new oracle as a permanent
guard (a clean hunt is a valid result — `#118` found none).

## Mergeability: simple fix vs. needs review

For each fix PR, classify how safe it is to land, so a trivial correctness fix
isn't held up and a behavior/spec change isn't waved through. State the verdict
on the **first line of the PR body** as `Mergeability: auto-merge-safe` or
`Mergeability: needs review — <reason>`, and only enable auto-merge for the
former (and only when the user has authorized auto-merge at all).

**Auto-merge-safe** — *all* of these hold:

- **No change for already-valid input.** The diff only changes output for input
  that was already broken (panic / invalid SQL / wrong result / leak); every
  previously-correct input renders byte-identically. The cleanest signal: the
  round-trip / semantic / golden oracles and the full suite are green with **no
  expected-output edits** beyond the new regression test.
- **No public-API or contract change** — no changed/added exported signature a
  caller must adopt, no change to a documented limitation (`docs/limitations.md`).
- **Self-contained** — one package, small diff, no embedded-output regeneration
  (no golden/binding churn — see `validation.md`), no new dependency.
- **Follows an established repo pattern** — e.g. raw-fallback for an unmodelable
  construct, or a compile-time rejection of dialect-invalid SQL of a kind the
  compiler already rejects elsewhere.
- **Not in a security/integrity-critical pass** — redaction masking, routing
  read/write split, transaction atomicity. Those get human eyes even when small.
- Regression test proven to fail without the fix; full validation green.

  *Examples from this skill's own round:* the GROUP_CONCAT raw-fallback and the
  timestamptz / schema-qualified-operator rendering fixes — each only turns
  broken output into correct output, follows the raw-fallback / canonical-render
  pattern, and touches one parser package.

**Needs review** (hold auto-merge, and call the reason out at the top of the PR)
— *any* of these:

- **It changes behavior for input that was previously accepted** — even if the
  old behavior was wrong. Introducing a **new error or rejection** where output
  used to be produced is a soft spec change: a caller may depend on the old
  path, so a human decides. *Example:* the negative-`LIMIT`/`OFFSET` fix now
  errors at compile time where it used to emit `LIMIT -1` — correct, but a
  behavior change worth a maintainer's nod.
- **It alters a contract or default** — a public API, a documented limitation, an
  error message other code matches on, or a serialized/encoded form (a checksum
  or fingerprint encoding change ripples to every consumer; see `validation.md`).
- **It touches security/integrity semantics** — redaction, routing, transactions.
- **Broad blast radius** — embedded-output regeneration across modules, or a
  cross-cutting change.
- **Ambiguous root cause or more than one plausible fix** — when the right
  behavior is a judgment call (which dialect is correct? reject vs. raw-fallback?),
  surface the options with `AskUserQuestion` rather than picking silently.

When in doubt, default to **needs review**: the cost of a held PR is a comment;
the cost of an auto-merged spec change is a regression in `main`.

## Labels

Label every PR so the queue is triageable at a glance — the same severity and
Mergeability verdicts, as labels. Apply them on creation (via the GitHub MCP
`issue_write` with `method: update` and the full `labels` array; a name that does
not exist yet is created automatically, defaulting to a gray swatch — fine to
leave). Each PR carries exactly one kind label, one severity (bug fixes only),
and one mergeability label:

- **Kind** — `bug` (a fix), `enhancement` (a feature/new API), or
  `documentation` (docs/skill only).
- **Severity** (bug fixes) — `severity: critical` / `severity: high` /
  `severity: medium` / `severity: low`, matching the Severity rubric above.
- **Mergeability** — `auto-merge-safe` or `needs-review`, matching the verdict
  above. A feature or a docs/conventions change defaults to `needs-review`.

So a typical silent-wrong-data fix that only corrects broken output is
`bug` + `severity: critical` + `auto-merge-safe`; a checksum-encoding change is
`bug` + `severity: critical` + `needs-review`.
