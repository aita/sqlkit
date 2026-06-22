# CLAUDE.md

このリポジトリで作業する際の規約。毎回の指示を不要にするために集約している。

## テストファイルの分け方

- **1ソース1テストファイル**: `foo.go` に対しては `foo_test.go` を対応させる。
  まずはこの対応を基本とし、テストを安易に1ファイルへ寄せ集めない。
- **機能・関心ごとに分割**: テストファイルが大きくなってきたら、機能（関心事）
  単位で `foo_<topic>_test.go` のように分割する。1ファイルに詰め込まない。
  - 例: `querytrace/warning_positive_test.go` /
    `querytrace/warning_derive_test.go` /
    `querytrace/warning_transaction_test.go` のように
    `warning` 機能を観点ごとに分けている。
- **Fuzz は別ファイル**: Fuzz テストは通常のテストと混ぜず、
  `foo_fuzz_test.go` に分離する（例: `paginator_fuzz_test.go`,
  `internal/reflectmap/snakecase_fuzz_test.go`）。
- **結合テストは別ファイル・別パッケージ**: 結合（integration）テストや
  ラウンドトリップ系は `internal/tests/` 配下に置く
  （例: `internal/tests/integration/`, `internal/tests/roundtrip/`）。
  ユニットテストと同じディレクトリに混在させない。

## ビルド・テスト・Lint コマンド

このリポジトリは複数の Go モジュールで構成される。ルートモジュールでは以下を使う。

```sh
go build ./...
go test -race ./...
go vet ./...
gofmt -l .            # 出力が空であること（未フォーマット検出）
golangci-lint run ./...
```

- 提出前には少なくとも `gofmt`・`go vet`・`go test -race ./...` を通す。
- `examples/` 配下はより厳しい `examples/.golangci.yml` が適用される。

## モジュール構成と依存方針

- ルート: `github.com/aita/sqlkit`（go 1.25）。利用者が import する中核モジュール。
- **ルートモジュールを軽量・cgo フリーに保つ**。重い依存や cgo を持つものは
  独立した leaf モジュールに隔離し、ルートへ依存が漏れないようにする。
  - `sqlparse/postgres`: pg_query_go（cgo）を使うため別モジュール。
  - `sqlparse/mysql`: TiDB パーサを使うため別モジュール。
  - `querytrace/otelquery`: OpenTelemetry 依存を切り離すため別モジュール。
  - `internal/tests/roundtrip`: ビルダ＋両パーサを必要とするラウンドトリップ
    オラクル。cgo/パーサ依存を抱えるため別モジュール。
- 新しい重量級依存を入れるときは、ルートに足さず別モジュール化を検討する。
- パッケージをまたぐ変更時は、該当 leaf モジュールでも個別に `go build/vet/test`
  すること（CI も各モジュールを別ジョブで回している）。

## コミットメッセージ

- Conventional Commits 形式・スコープ付きを使う:
  `feat(scope): ...` / `fix(scope): ...` / `build: ...` / `chore: ...`。
  スコープは変更したパッケージ名（例: `fix(codegen)`,
  `feat(sqlparse/postgres)`, `redact: ...`）。
- 1コミット1関心。要約は命令形・現在形で簡潔に。

## ドキュメント

- 振る舞いを変えたら `docs/` 配下の該当トピック（`query-builder.md`,
  `migrations.md`, `redaction.md`, `routing.md`, `observability.md`,
  `schema-codegen.md`, `testing.md` など）も更新する。
- コード生成を伴う変更（`go:generate` / `cmd/sqlkit`）では、生成物の
  再生成とコミットを忘れない。
