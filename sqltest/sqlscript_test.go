package sqltest

import (
	"reflect"
	"testing"
)

func TestSplitScript(t *testing.T) {
	cases := []struct {
		name   string
		script string
		mysql  bool
		want   []string
	}{
		{"semicolon in string", "CREATE TABLE t (s text default 'a;b'); SELECT 1", false,
			[]string{"CREATE TABLE t (s text default 'a;b')", "SELECT 1"}},
		{"doubled quote", "SELECT 'it''s;x'; SELECT 2", false, []string{"SELECT 'it''s;x'", "SELECT 2"}},
		{"quoted identifier", `CREATE TABLE "a;b" (id int); SELECT 1`, false, []string{`CREATE TABLE "a;b" (id int)`, "SELECT 1"}},
		{"line comment", "SELECT 1; -- drop; this\nSELECT 2", false, []string{"SELECT 1", "-- drop; this\nSELECT 2"}},
		{"block comment", "SELECT 1 /* a; b */; SELECT 2", false, []string{"SELECT 1 /* a; b */", "SELECT 2"}},
		{"dollar body", "CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql; SELECT 2", false,
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql", "SELECT 2"}},
		{"tagged dollar body", "DO $x$ BEGIN PERFORM 1; END $x$; SELECT 2", false, []string{"DO $x$ BEGIN PERFORM 1; END $x$", "SELECT 2"}},
		{"positional param is not a tag", "SELECT $1; SELECT $2", false, []string{"SELECT $1", "SELECT $2"}},
		{"comment-only statement dropped", "SELECT 1; -- trailing\n", false, []string{"SELECT 1"}},
		{"mysql backslash escape", `SELECT 'a\';b'; SELECT 2`, true, []string{`SELECT 'a\';b'`, "SELECT 2"}},
		{"mysql hash comment", "SELECT 1; # x; y\nSELECT 2", true, []string{"SELECT 1", "# x; y\nSELECT 2"}},
		{"mysql backtick", "CREATE TABLE `a;b` (id int); SELECT 1", true, []string{"CREATE TABLE `a;b` (id int)", "SELECT 1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, s := range splitScript(c.script, c.mysql) {
				got = append(got, s.text)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("splitScript(%q) = %q, want %q", c.script, got, c.want)
			}
		})
	}
}

func TestCreatedTableNames(t *testing.T) {
	cases := []struct {
		name   string
		script string
		mysql  bool
		want   []string
	}{
		{"comment mentioning create table", "-- create table backups later\nCREATE TABLE users (id int)", false, []string{"users"}},
		{"postgres folds unquoted", `CREATE TABLE Users (id int); CREATE TABLE "Mixed" (id int); CREATE TABLE App.Items (id int)`, false,
			[]string{"users", "Mixed", "app.items"}},
		{"mysql keeps case", "CREATE TABLE Users (id int); CREATE TABLE `Mixed` (id int)", true, []string{"Users", "Mixed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := createdTableNames(splitScript(c.script, c.mysql), c.mysql); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("createdTableNames = %q, want %q", got, c.want)
			}
		})
	}
}
