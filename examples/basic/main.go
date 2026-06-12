// Command basic demonstrates the typed sqlkit query builder with a
// hand-written table binding. Real projects generate the binding from a live
// PostgreSQL schema with sqlkitgen; see examples/blog.
package main

import (
	"fmt"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

// User is the row struct for the "users" table.
type User struct {
	ID    int64
	Email string
}

// UsersTable is the typed binding for the "users" table.
type UsersTable struct {
	info  sqlkit.TableInfo
	ID    sqlkit.Column[int64]
	Email sqlkit.Column[string]
}

// Users is the typed accessor for the "users" table.
var Users = func() UsersTable {
	info := sqlkit.TableInfo{Name: "users"}
	return UsersTable{
		info:  info,
		ID:    sqlkit.NewColumn[int64](info, "id"),
		Email: sqlkit.NewColumn[string](info, "email"),
	}
}()

func (t UsersTable) Info() sqlkit.TableInfo             { return t.info }
func (t UsersTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t UsersTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Email} }
func (t UsersTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t UsersTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }

func main() {
	// Type-checked at compile time: Users.ID.Eq("foo") would not compile.
	query := sqlkit.Select().From(Users).
		Where(Users.ID.Eq(42), sqlkit.Like(Users.Email, "%@example.com")).
		OrderBy(Users.Email.Asc()).
		Limit(10)

	sqlText, args, err := query.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		panic(err)
	}
	fmt.Println(sqlText)
	fmt.Println(args)
}
