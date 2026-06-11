package main

import (
	"fmt"

	"github.com/sqlkit-dev/sqlkit/sqlkit/meta"
	"github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

type User struct {
	ID    int
	Email string
}

type UsersTable struct {
	meta.Table
	ID    meta.Column
	Email meta.Column
}

var Users = UsersTable{
	Table: meta.DefineTable("users"),
}

func init() {
	Users.ID = Users.Column("id", meta.PrimaryKey(), meta.WithGoType(int(1)))
	Users.Email = Users.Column("email", meta.Varchar(255), meta.WithGoType("string"))
}

func main() {
	query := sql.Select(Users.ID, Users.Email).
		From(Users).
		Where(sql.Eq(Users.ID, sql.Int(42)))

	sqlText, args, err := query.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		panic(err)
	}
	fmt.Println(sqlText)
	fmt.Println(args)
}
