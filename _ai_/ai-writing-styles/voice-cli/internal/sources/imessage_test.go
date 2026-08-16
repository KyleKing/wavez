package sources

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newFakeChatDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := `
	CREATE TABLE message (
		ROWID INTEGER PRIMARY KEY,
		text TEXT,
		attributedBody BLOB,
		date INTEGER,
		is_from_me INTEGER
	);
	CREATE TABLE chat (
		ROWID INTEGER PRIMARY KEY,
		chat_identifier TEXT
	);
	CREATE TABLE chat_message_join (
		chat_id INTEGER,
		message_id INTEGER
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}

	insertChat := `INSERT INTO chat (ROWID, chat_identifier) VALUES (1, 'friend@example.com')`
	if _, err := db.Exec(insertChat); err != nil {
		t.Fatalf("inserting chat: %v", err)
	}

	messages := []struct {
		rowID    int64
		text     any
		hasBody  bool
		date     int64
		isFromMe int
	}{
		{1, "hello there", false, 700000000000000000, 1}, // plain text, from me, nanoseconds
		{2, nil, true, 700000000, 1},                     // attributedBody only, from me, seconds
		{3, "not mine", false, 700000000000000000, 0},    // not from me, excluded by query
	}

	for _, m := range messages {
		if _, err := db.Exec(
			`INSERT INTO message (ROWID, text, attributedBody, date, is_from_me) VALUES (?, ?, ?, ?, ?)`,
			m.rowID, m.text, boolToBlob(m.hasBody), m.date, m.isFromMe,
		); err != nil {
			t.Fatalf("inserting message %d: %v", m.rowID, err)
		}
		if _, err := db.Exec(
			`INSERT INTO chat_message_join (chat_id, message_id) VALUES (1, ?)`, m.rowID,
		); err != nil {
			t.Fatalf("inserting chat_message_join for %d: %v", m.rowID, err)
		}
	}

	return db
}

func boolToBlob(hasBody bool) []byte {
	if !hasBody {
		return nil
	}
	return []byte{0x01}
}

func TestFetchIMessagesFromDB(t *testing.T) {
	db := newFakeChatDB(t)

	candidates, skipped, err := fetchIMessagesFromDB(db, 10)
	if err != nil {
		t.Fatalf("fetchIMessagesFromDB() error = %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].Text != "hello there" {
		t.Errorf("Text = %q, want %q", candidates[0].Text, "hello there")
	}
	if candidates[0].Context != "friend@example.com" {
		t.Errorf("Context = %q, want %q", candidates[0].Context, "friend@example.com")
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

func TestMacAbsoluteTimeToTime(t *testing.T) {
	got := macAbsoluteTimeToTime(0)
	want := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("macAbsoluteTimeToTime(0) = %v, want %v", got, want)
	}

	// One mac-epoch second, expressed in the legacy seconds format.
	got = macAbsoluteTimeToTime(1)
	want = macEpoch.Add(time.Second)
	if !got.Equal(want) {
		t.Errorf("macAbsoluteTimeToTime(1) = %v, want %v", got, want)
	}
}
