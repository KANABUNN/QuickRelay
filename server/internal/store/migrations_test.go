package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeMigrationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// A real older database, already containing a paired device.
	initial, err := migrations.ReadFile("migrations/001_devices.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE schema_migrations(name TEXT PRIMARY KEY);" + string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO schema_migrations VALUES('001_devices.sql')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ctx := context.Background()
	code, err := st.CreatePairing(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.Pair(ctx, code, "retained-phone", strings.Repeat("s", 32), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ingest(ctx, report("retained-report", "VXSE45", 4, now)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var count int
	var mode, integrity string
	if err = st.DB.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	if err = st.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal(mode, err)
	}
	if err = st.DB.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	if id, err := st.Authenticate(ctx, token); err != nil || id != "retained-phone" {
		t.Fatal(id, err)
	}
	page, err := st.Sync(ctx, 0, 10)
	if err != nil || len(page.Items) != 1 || *page.Items[0].Event.SourceSerial != 4 {
		t.Fatal(page, err)
	}
}
