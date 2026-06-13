package main

import (
	"strings"
	"testing"
)

func TestRenderBridge(t *testing.T) {
	src, err := renderBridge(bridgeData{
		ImportPath: "example.com/app/migrations",
		Registry:   "Registry",
		Driver:     "pgx",
		DriverPkg:  "github.com/jackc/pgx/v5/stdlib",
	})
	if err != nil {
		t.Fatalf("renderBridge: %v", err)
	}
	got := string(src)
	for _, want := range []string{
		`_ "github.com/jackc/pgx/v5/stdlib"`,
		`usermigrations "example.com/app/migrations"`,
		`sqlkit.Open("pgx", os.Getenv("SQLKIT_MIGRATE_DSN"))`,
		`usermigrations.Registry.NewMigrator(`,
		`m.RunCLI(context.Background(), os.Args[1:])`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated program missing %q:\n%s", want, got)
		}
	}
}

func TestRunValidatesFlags(t *testing.T) {
	cases := []struct {
		name string
		f    bridgeFlags
		want string
	}{
		{
			name: "missing dsn",
			f:    bridgeFlags{Registry: "Registry", Driver: "pgx", DriverPkg: "x/y"},
			want: "DATABASE_URL",
		},
		{
			name: "bad registry",
			f:    bridgeFlags{DSN: "x", Registry: "not an ident", Driver: "pgx", DriverPkg: "x/y"},
			want: "registry",
		},
		{
			name: "bad driver",
			f:    bridgeFlags{DSN: "x", Registry: "Registry", Driver: "bad driver", DriverPkg: "x/y"},
			want: "driver",
		},
		{
			name: "bad driver-pkg",
			f:    bridgeFlags{DSN: "x", Registry: "Registry", Driver: "pgx", DriverPkg: "bad pkg!"},
			want: "driver-pkg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.f.run("up")
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}
