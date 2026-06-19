package reflectmap

import "testing"

func TestSnakeCase(t *testing.T) {
	cases := []struct{ in, out string }{
		{"UserID", "user_id"},
		{"CreatedAt", "created_at"},
		{"HTTPCode", "http_code"},
		{"ID", "id"},
		{"Email", "email"},
		{"ParseURL", "parse_url"},
		{"already_snake", "already_snake"},
	}
	for _, c := range cases {
		if got := SnakeCase(c.in); got != c.out {
			t.Errorf("SnakeCase(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}
