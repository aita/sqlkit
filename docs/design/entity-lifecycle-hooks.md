# 決定記録: Entity ライフサイクルフックは採用しない

ステータス: **採用しない (Rejected)**
対象: エンティティ単位の Before/After Insert・Update・Delete・Load フック全般

## 結論

sqlkit は、Ent の `BeforeCreate`/`AfterCreate`、GORM の `BeforeSave`/`AfterFind`、
SQLAlchemy の `before_insert`/`load` に相当する**エンティティ単位の
ライフサイクルフックを提供しない**。これは未実装の機能ではなく、設計上の
**意図的な不在**である。

理由は一言で言えば、ライフサイクルフックが**暗黙的(マジック)な機能**であり、
sqlkit が一貫して採ってきた明示主義と衝突するためである。

## なぜ採用しないか

1. **暗黙の副作用は本ライブラリの方針に反する。**
   フックは「`Insert` したら裏で `BeforeInsert` が走る」という暗黙の制御フローを
   生む。sqlkit は逆に、列の符号化は `sql.Scanner`/`driver.Valuer` に、文の観測・
   書き換えは `Interceptor` に、結果の観測は `ResultObserver` にと、拡張点を
   **明示的に呼び出し側へ見せる**設計を貫いている（`observer.go` の
   「値レベル変換は `sql.Scanner` を実装せよ」という指針が象徴的）。フックは
   この明示主義を崩す。

2. **発火点を一貫させられない。**
   エンティティ instance が identity 付きで存在するのは、`Insert.Values`(`q.rows`)と
   スキャン境界(各行)だけである。`Update(t).Set(...).Where(...)` や
   `Delete(t).Where(...)` は SQL 粒度で、行を struct として materialize しないため
   instance 単位フックを発火できない。Insert/Load だけ発火し Update/Delete は
   発火しない、という非対称は「どの操作でフックが走るか」を予測困難にし、
   暗黙性をさらに悪化させる。

3. **既存の手段で十分まかなえる。**
   - 読み込み後の検査・統計: `ResultObserver`（`WithResultObserver`）。
   - SQL の観測・書き換え（テナント注入、タイムスタンプの SQL 側付与など）:
     `Interceptor`（`WithInterceptor`、または `Rewrite`）。
   - 列の符号化/復号: `sql.Scanner` / `driver.Valuer` をエンティティの列型に実装。
   - エンティティ単位の保存・差分・順序づけ・生成キー書き戻し: `uow.Scope`
     （スナップショット差分。フラッシュ前後の処理は呼び出し側が**明示的に**書く）。

4. **明示的なコードで代替できる。**
   「INSERT 前にタイムスタンプを埋める」「保存前に検証する」といった処理は、
   呼び出し側が `Exec` の前後に普通の Go コードとして書けばよく、暗黙のフックに
   する必要がない。明示的な方が、どの順序で何が走るかがコード上で追える。

```go
// フックではなく、呼び出し側で明示する。
u.CreatedAt = time.Now()
if err := validate(u); err != nil {
    return err
}
if _, err := db.Insert(blogdb.Users).Values(u).Exec(ctx); err != nil {
    return err
}
```

## 検討した代替案（いずれも不採用）

- **`uow.Scope` のフラッシュ境界で発火**: `uow` は唯一のエンティティ単位エンジン
  だが、フックという暗黙機能を `uow` に持たせること自体が方針に反するため不採用。
- **ビルダー/実行境界で発火**: 上記 2 の非対称（Update/Delete で発火不能）により
  一貫性が取れず不採用。
- **`Database` への中央登録（`WithEntityHook`）**: 文粒度になりエンティティ単位の
  意味を持てず、`Interceptor` の再発明にしかならないため不採用。

## まとめ

エンティティ単位の前後処理が必要な場合は、フックという暗黙の仕組みではなく、
`Interceptor` / `ResultObserver` / `sql.Scanner` / `uow.Scope` のいずれかと、
呼び出し側の明示的なコードで実現する。本決定はこの方針を将来に残すための記録で
あり、同じ提案が再び持ち上がったときの参照点とする。
