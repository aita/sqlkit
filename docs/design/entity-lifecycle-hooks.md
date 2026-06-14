# 設計提案: Entity ライフサイクルフック

ステータス: ドラフト / レビュー募集
対象: `uow` パッケージ、`structscan.go`（読み込み境界）、`codegen`（任意）

## 1. 背景

sqlkit における「Entity（エンティティ）」とは、`codegen` が各テーブルに対して
生成する**行構造体**（`User`、`Post` …）のことである。これらは `sqlkit:` タグを
持つだけのプレーンな DTO で、メソッドも基底型も DB への逆参照も持たない
（`codegen/testdata/blogdb.go.golden` 参照）。

```go
type User struct {
    ID        int64     `sqlkit:"id"`
    Email     string    `sqlkit:"email"`
    Name      *string   `sqlkit:"name"`
    CreatedAt time.Time `sqlkit:"created_at"`
}
```

現状、sqlkit が用意している拡張点はいずれも**文（statement）粒度**であり、
エンティティ単位のフックは存在しない。

| 拡張点 | 粒度 | 位置 | 役割 |
|---|---|---|---|
| `Interceptor` | 1 文 | 実行チェーン（SQL レベル） | SQL/Args の観測・書き換え・短絡・リトライ |
| `ResultObserver` | 1 結果セット | スキャン直後 | 読み取り結果の観測（read-only、変更禁止） |

一方で「INSERT する直前に `CreatedAt` を埋める」「保存前にバリデーションする」
「読み込み後に派生フィールドを計算する」「削除前に関連を片付ける」といった
**エンティティ単位の関心事**は、文粒度のフックでは表現できない。Ent の
`BeforeCreate`/`AfterCreate`、GORM の `BeforeSave`/`AfterFind`、SQLAlchemy の
`before_insert`/`load` に相当する層が抜けている。本提案はその層を設計する。

## 2. 設計目標 / 非目標

リポジトリの既存方針（値型中心・逆参照なし・「値レベル変換は `sql.Scanner` に
寄せる」という明示主義・gRPC 風ミドルウェア）に合わせる。

**目標**

1. エンティティ実装側にフックを定義させる（中央レジストリ不要）。`sql.Scanner` を
   エンティティに実装させる既存の慣習と同じ流儀にする。
2. **オプトイン**。フックを実装しないエンティティには一切のコスト・挙動変化を
   与えない（`ResultObserver` が未登録時に完全に no-op であるのと同じ精神）。
3. 既存の `Interceptor` / `ResultObserver` と**責務が重ならない**こと。フックは
   「エンティティ instance とその identity」を見る層であり、SQL を見る層ではない。
4. トランザクション境界・エラー伝播・実行順序が明確で予測可能であること。

**非目標**

- 文粒度フック（既に `Interceptor` がある）の再発明。
- 列値の変換（既に `sql.Scanner`/`driver.Valuer` がある）。フックは
  エンティティ単位の副作用・検証・派生計算のためであり、列の符号化のためではない。
- フィールド代入の捕捉。Go では不可能であり、`uow` も既にこの前提
  （スナップショット差分はフラッシュ時に計算）で設計されている。

## 3. なぜ `uow.Scope` がディスパッチ点なのか

書き込み系フック（Insert/Update/Delete）の発火点は **`uow.Scope` のフラッシュ
境界**を第一候補とする。理由は、`uow.Scope` が sqlkit で唯一の
**エンティティ単位エンジン**だからである。

- `Scope` は構造体ポインタを保持し（`tracked.ptr`）、列スナップショットを取り、
  差分を計算し、生成キーを書き戻し、フラッシュを順序づける（`uow/scope.go`）。
  つまり「identity を持つエンティティ instance」がそこに存在する唯一の場所。
- 対照的にビルダー直叩き（`db.Insert(Users).Values(u).Exec`）はリフレクションで
  struct → 値タプルへ写したあと**元の instance を捨てる**（`insert.go` の
  `mapRows`）。`UPDATE ... WHERE` に至っては行が struct として materialize すら
  されない。ここでエンティティ単位フックを一貫して発火させることはできない。

読み込み系フック（AfterLoad）だけは `uow` 外の経路（`db.Select().Scan(...)`）でも
意味を持つため、**スキャン境界**（`structscan.go`）でも発火させる。これは
`ResultObserver` を `Scan` の直後に呼ぶ既存配置（`exec.go:217`）と対称になる。

## 4. 提案 API: エンティティ実装インターフェース

`sql.Scanner` と同じく、**エンティティが任意に実装するインターフェース**として
定義する。`uow`（書き込み系）と sqlkit 本体（AfterLoad）が型アサーションで
検出する。フックを実装しない型に対しては型アサーションが失敗するだけで、
追加コストはゼロ。

```go
// hooks.go（uow パッケージ、書き込み系）

// BeforeInserter is implemented by an entity that runs logic just before it is
// INSERTed during a Scope flush — defaulting timestamps, generating a UUID,
// validating. Returning an error aborts the flush before the row is written.
type BeforeInserter interface{ BeforeInsert(ctx context.Context) error }

// AfterInserter runs just after the entity's INSERT, within the same
// transaction, after generated keys/defaults have been written back.
type AfterInserter interface{ AfterInsert(ctx context.Context) error }

// BeforeUpdater runs just before a managed/staged entity's UPDATE. It sees the
// entity with its pending mutations; returning an error aborts the flush.
type BeforeUpdater interface{ BeforeUpdate(ctx context.Context) error }

// AfterUpdater runs just after the entity's UPDATE, within the same transaction.
type AfterUpdater interface{ AfterUpdate(ctx context.Context) error }

// BeforeDeleter runs just before the entity's DELETE; returning an error aborts.
type BeforeDeleter interface{ BeforeDelete(ctx context.Context) error }

// AfterDeleter runs just after the entity's DELETE, within the same transaction.
type AfterDeleter interface{ AfterDelete(ctx context.Context) error }
```

```go
// AfterLoader（sqlkit 本体）は読み込み境界で発火する。
// AfterLoad runs once per row right after it is scanned into the entity, on
// every read path (db.Select(...).Scan, uow Get/Find, RawSQL into a struct). Use
// it to compute derived fields or decrypt a column after load. Unlike a
// ResultObserver — which sees the whole materialized destination and must not
// mutate it — AfterLoad sees a single entity and may mutate it in place.
type AfterLoader interface{ AfterLoad(ctx context.Context) error }
```

設計上の選択:

- **動詞は SQL に合わせる**（`Insert`/`Update`/`Delete`）。`uow` は `Add`/`Remove`、
  SQL レイヤは `OpExec` を使うが、フック名は最も曖昧さのない SQL 動詞に揃える。
  GORM 風の `BeforeSave`（Insert と Update をまたぐ）は**第 2 フェーズ**の任意拡張
  とし、まず原子的な 6 + 1 個に絞る。
- **シグネチャは `ctx` のみ**。`sql.Scanner` のミニマル主義に合わせ、エンティティ
  パッケージが `context` 以外を import せずに済むようにする。フック内から DB に
  アクセスしたい場合は §6 の「ctx 経由の Querier 注入」を使う。

### フックを束ねるヘルパー（内部）

`uow` 内では各フックの呼び出しを 1 か所に集約する:

```go
func runBeforeInsert(ctx context.Context, ptr any) error {
    if h, ok := ptr.(BeforeInserter); ok {
        return h.BeforeInsert(ctx)
    }
    return nil
}
// AfterInsert / Before|AfterUpdate / Before|AfterDelete も同様
```

ポインタレシーバで実装するのが自然なため（フックは大抵 instance を書き換える）、
`uow` は常に**ポインタ**に対して型アサーションする。値で `Add` された行については
発火条件を §6 で定める。

## 5. 発火点とフラッシュ統合

`Scope.flush`（`uow/scope.go:399`）は現状テーブル順に
inserts → managed → updates → removals を実行する。フックは各操作の前後に挿入する。

```
flush(ctx):
  for table in order:
    for each insert row:   BeforeInsert → INSERT(+writeback) → AfterInsert
    for each managed/update row(差分あり): BeforeUpdate → UPDATE → AfterUpdate
    for each removal:      BeforeDelete → DELETE → AfterDelete
```

ただし `uow` は性能のため UPDATE/DELETE を**バッチ**にまとめる
（`flushBulkUpdates` の VALUES-join、`flushRemovals` の `IN (...)`）。フックとの
整合は次の規則で取る:

- **Before フックはバッチ構築の前に、対象 instance ごとに**実行する。これにより
  Before フックが起こした変更（例: `UpdatedAt` の更新）が差分計算に反映され、
  実際に書かれる SET 句へ正しく入る。**重要な含意**: Before フックは差分計算より
  前に走る必要があるため、`flushManaged` のループ内、`ColumnValues` 取得の**前**に
  挿入する。
- **After フックはバッチ実行の後に、対象 instance ごとに**実行する。バッチに
  まとめられても「各エンティティの After が、その書き込みがコミット前に成功裏に
  発行された後で 1 回ずつ走る」という観測上の保証を与える。
- `removed` は現状 PK だけを保持し instance を保持しない（`removal{pk}`）。
  Before/AfterDelete を instance フックにするには、`Remove` で渡された行ポインタを
  `removal` に保持する小改修が要る（§9 実装計画）。

### 5.1 ビルダー直叩き経路（uow を使わない場合）

`db.Insert(Users).Values(u).Exec` のような直叩き経路では、原則として
**書き込み系フックは発火しない**。理由は §3 のとおり instance の identity が
保持されないため。ただし利用者の驚きを減らすため、次のいずれかを選ぶ
（オープン論点 §10）:

- (A) **発火しない**と明文化し、エンティティ単位の副作用が必要なら `uow` を使う、
  という線引きにする（最も単純・最も明示的、本提案の推奨）。
- (B) `Values`/`Returning` の write-back 経路に限り、渡された instance に対して
  Before/AfterInsert を発火する。`UPDATE/DELETE ... WHERE` は対象外のまま。
  → 一貫性が崩れる（Insert だけ特別扱い）ため非推奨。

AfterLoad は経路に依存せずスキャン境界で発火するので、この問題の影響を受けない。

## 6. セマンティクス

**実行順序**
同一テーブル内ではフラッシュの確定順（insert → update → delete）に従い、
テーブル間は `Scope.order`（最初に触れた順）に従う。これは既存のフラッシュ
順序をそのまま継承し、フックのために新しい順序規則を導入しない。

**エラー伝播**
- Before フック / AfterLoad がエラーを返したら、その操作を**中止**して
  `flush`（または `Scan`）から即座に返す。ネストスコープなら savepoint へ
  ロールバック、ルートスコープなら呼び出し側（典型的には `WithSession`）が
  トランザクションをロールバックする（`db.go:363` の挙動）。
- After フックがエラーを返した場合も同様にフラッシュを中止する。書き込み自体は
  発行済みだが**まだコミットされていない**ため、トランザクションのロールバックで
  巻き戻る。これは「After フックはコミットの一部」というモデルを与える
  （SQLAlchemy の `after_*` がフラッシュ内・コミット前に走るのと同じ）。
- フックを「ベストエフォート（失敗してもフラッシュ継続）」にはしない。明示的な
  エラーモデルの方が、この library の他の部分と整合する。

**トランザクション**
全フックは `Scope` がフラッシュに使う `Querier`（推奨は `*sqlkit.Session`）の
**同一トランザクション内**で走る。AfterLoad はその読み取りを行ったトランザクション
（あれば）の中で走る。

**フック内からの DB アクセス（任意・第 2 フェーズ）**
フックが同一トランザクションで追加クエリを発行したいケース（例: 派生集計、
カスケード）に対しては、`Scope` がフラッシュ中の `ctx` に自身の `Querier` を
載せ、フック側がヘルパーで取り出せるようにする:

```go
// In flush, before invoking hooks:
ctx = uow.WithQuerier(ctx, s.ex)

// In a hook:
func (u *User) BeforeInsert(ctx context.Context) error {
    q, _ := uow.QuerierFrom(ctx) // same transaction as the flush
    ...
}
```

`context` 経由にすることで、フックを持たないエンティティパッケージが
`sqlkit.Querier` に依存せずに済む（`sql.Scanner` のミニマル主義を保つ）。
代償としてフックからの**再入フラッシュは禁止**する（`Scope` に
`flushing` フラグを立て、再入時に明示エラーを返す）。

**冪等性とリトライ**
`Interceptor` は `next` を複数回呼べる（リトライ）。フックはエンティティ単位の
副作用を持ちうるため**リトライで複数回呼ばれうる**ことを明記し、フック実装側に
冪等性を求める。`uow` 自身はフックを 1 操作につき 1 回だけ呼ぶ。

## 7. 利用例

```go
// 生成された行構造体に、利用者がフックだけを別ファイルで足す。
func (u *User) BeforeInsert(ctx context.Context) error {
    if u.CreatedAt.IsZero() {
        u.CreatedAt = time.Now()
    }
    if u.Email == "" {
        return errors.New("user: email required")
    }
    return nil
}

func (u *User) AfterLoad(ctx context.Context) error {
    u.displayName = derive(u.Name) // 非永続の派生フィールド
    return nil
}

err := db.WithSession(func(s *sqlkit.Session) error {
    scope := uow.NewScope(s)
    scope.Add(blogdb.Users, &u) // BeforeInsert → INSERT → AfterInsert がフラッシュで走る
    return scope.Flush(ctx)
})
```

## 8. 代替案の検討

1. **中央レジストリ方式**（`WithEntityHook(Users, fn)` を `Database` に登録）。
   → 既存の `WithInterceptor`/`WithResultObserver` と形は揃うが、エンティティと
   フックが分離して見通しが悪く、`sql.Scanner` の流儀（型に実装させる）から外れる。
   フックがエンティティの不変条件である以上、型に置く方が凝集度が高い。**不採用**。
2. **構造体タグ方式**（`sqlkit:"created_at,touch_on_insert"`）。
   → 表現力が乏しく、任意ロジックを書けない。タイムスタンプ程度なら有用だが、
   フックの代替にはならない。将来 `codegen` 側の糖衣として併設可能。
3. **ビルダー層で全経路フック**（GORM 流に `Exec` でも発火）。
   → §3 のとおり `UPDATE ... WHERE` 等で instance が無く一貫させられない。
   sqlkit の「ビルダーは SQL 粒度」という設計と衝突する。**不採用**。

## 9. 段階的実装計画

- **フェーズ 1（最小・破壊的変更なし）**
  - `structscan.go` に `AfterLoader` 検出を追加。`structScanAll`/`structScanOne` の
    各行スキャン直後（`exec.go` の `observeResult` 呼び出しと同じ層が望ましいが、
    要素単位なのでスキャン側）で型アサーションし発火。要素型が `AfterLoader` を
    実装しない場合は一度の型チェックで終わる（ゼロコスト）。
  - `uow` に `BeforeInserter`/`AfterInserter` と `runBefore/AfterInsert` を追加し、
    `flushInserts`/`insertWithWriteback` に挿入。
- **フェーズ 2**
  - `BeforeUpdater`/`AfterUpdater` を `flushManaged`/`flushUpdates` に挿入
    （Before は差分計算の前）。
  - `removal` に行ポインタを保持させ `Before/AfterDeleter` を `flushRemovals` に挿入。
- **フェーズ 3（任意）**
  - `uow.WithQuerier`/`QuerierFrom` と再入ガード（`flushing` フラグ）。
  - `BeforeSaver`/`AfterSaver`（Insert∪Update をまたぐ糖衣）。
  - `codegen` に「フック用の `*_hooks.go` スタブを生成」オプション（任意）。

各フェーズは独立してマージ可能で、フックを実装しない既存コードには影響しない。

## 10. オープンな論点

1. §5.1 (A)/(B): ビルダー直叩きで Insert フックを発火させるか。推奨は (A)。
2. After フックのエラーで全体をロールバックする方針でよいか（本提案は yes）。
3. フックのシグネチャを `ctx` のみにするか、明示的に `Querier` を渡すか。
   本提案は `ctx` のみ＋ctx 注入。
4. AfterLoad を `Preload` で読み込まれた関連エンティティにも適用するか
   （`applyPreloads` 経由。適用するのが直感的だが、発火点の追加実装が要る）。
