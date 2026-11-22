package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestDatabaseSQLQuoting(t *testing.T) {
	if got := sqlIdent("we`ird"); got != "`we``ird`" {
		t.Fatalf("ident %s", got)
	}
	if got := sqlString("it's\\ \x00\n"); got != `'it\'s\\ \0\n'` {
		t.Fatalf("string %s", got)
	}
	if got := sqlAccount("bob", "%"); got != `'bob'@'%'` {
		t.Fatalf("account %s", got)
	}
}

func TestDatabaseRedactsPasswords(t *testing.T) {
	for in, want := range map[string]string{
		"CREATE USER 'a'@'%' IDENTIFIED BY 'hunter2'":                        "CREATE USER 'a'@'%' IDENTIFIED BY '***'",
		"alter user x identified by 'it''s \\' secret'; SELECT 1":            "alter user x identified by '***'; SELECT 1",
		"SET PASSWORD FOR 'a'@'%' = PASSWORD('pw')":                          "SET PASSWORD FOR 'a'@'%' = PASSWORD('***')",
		"GRANT USAGE ON *.* TO `a`@`%` IDENTIFIED BY PASSWORD '*ABCDEF0123'": "GRANT USAGE ON *.* TO `a`@`%` IDENTIFIED BY PASSWORD '***'",
	} {
		got := redactSQL(in)
		if got != want {
			t.Errorf("redact %q = %q, want %q", in, got, want)
		}
	}
}

func TestDatabasePrivilegesAndGrantParsing(t *testing.T) {
	if _, err := cleanPrivileges([]string{"select", "SUPER"}); err == nil {
		t.Fatal("SUPER is not a database privilege")
	}
	got, err := cleanPrivileges([]string{"update", " select ", "SELECT"})
	if err != nil || strings.Join(got, ",") != "SELECT,UPDATE" {
		t.Fatalf("clean %v %v", got, err)
	}
	m := grantOnDB.FindStringSubmatch("GRANT SELECT, INSERT ON `jvs``db`.* TO `bob`@`%`")
	if m == nil || m[1] != "SELECT, INSERT" || m[2] != "jvs``db" {
		t.Fatalf("grant parse %v", m)
	}
	if err := checkAccount("bob;drop", "%"); err == nil {
		t.Fatal("unsafe username accepted")
	}
	if err := checkAccount("bob", "10.0.%"); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseManagerNeedsAnInstalledDatabase(t *testing.T) {
	_, _, id, call, _ := gmodTestServer(t, nil)
	if code, _ := call("POST", "/api/v1/game-servers/"+id+"/database/query", `{"sql":"SELECT 1"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("query without a database %d", code)
	}
	if code, out := call("GET", "/api/v1/databases", ""); code != http.StatusOK || out["items"] == nil {
		t.Fatalf("global list %d %v", code, out)
	}
	if code, _ := call("POST", "/api/v1/databases/bulk", `{"server_ids":["`+id+`"],"action":"drop"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown bulk action %d", code)
	}
}
