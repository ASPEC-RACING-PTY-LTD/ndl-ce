package appdb

import (
	"strings"
	"testing"
)

func TestListAuditSQLNeverComparesUUIDToEmptyLiteral(t *testing.T) {
	if strings.Contains(listAuditSQL, "cluster_id=$1 OR") {
		t.Fatal("legacy cluster_id=$1 OR ($1='') predicate reintroduced")
	}
	if !strings.Contains(listAuditSQL, "$1::text") {
		t.Fatal("list audit SQL must keep the cluster bind as text")
	}
	if !strings.Contains(listAuditSQL, "$1::uuid") {
		t.Fatal("list audit SQL must cast a non-empty cluster bind to uuid")
	}
}
