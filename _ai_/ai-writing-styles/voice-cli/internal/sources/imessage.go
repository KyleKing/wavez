package sources

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kyleking/voice-cli/internal/corpus"
)

// macEpoch is the Core Data / Cocoa reference date, 2001-01-01T00:00:00Z, the
// zero point for message.date in chat.db.
var macEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

const imessageQuery = `
SELECT
	message.ROWID,
	message.text,
	message.attributedBody IS NOT NULL AS has_attributed_body,
	message.date,
	chat.chat_identifier
FROM message
JOIN chat_message_join ON chat_message_join.message_id = message.ROWID
JOIN chat ON chat.ROWID = chat_message_join.chat_id
WHERE message.is_from_me = 1
ORDER BY message.date DESC
LIMIT ?`

// FetchIMessages opens dbPath (typically ~/Library/Messages/chat.db)
// read-only and returns candidates for every outgoing (is_from_me = 1)
// message with a plain-text body, up to limit rows. Messages that only have
// an attributedBody (rich text, requiring NSKeyedArchiver/typedstream
// decoding) are skipped; the count of skipped rows is logged via the
// returned error-free path by the caller inspecting the second return value.
func FetchIMessages(dbPath string, limit int) ([]corpus.Candidate, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&immutable=1", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening chat.db at %q: %w", dbPath, err)
	}
	defer db.Close()

	candidates, skipped, err := fetchIMessagesFromDB(db, limit)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		fmt.Printf("imessage: skipped %d message(s) with unsupported attributedBody-only content\n", skipped)
	}
	return candidates, nil
}

// fetchIMessagesFromDB runs the query against an already-open *sql.DB so unit
// tests can exercise it against an in-memory sqlite database shaped like the
// real chat.db schema, without needing Full Disk Access to real Messages data.
func fetchIMessagesFromDB(db *sql.DB, limit int) ([]corpus.Candidate, int, error) {
	rows, err := db.Query(imessageQuery, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("querying message table: %w", err)
	}
	defer rows.Close()

	var candidates []corpus.Candidate
	var skipped int

	for rows.Next() {
		var (
			rowID             int64
			text              sql.NullString
			hasAttributedBody bool
			date              int64
			chatIdentifier    string
		)
		if err := rows.Scan(&rowID, &text, &hasAttributedBody, &date, &chatIdentifier); err != nil {
			return nil, 0, fmt.Errorf("scanning message row: %w", err)
		}

		if !text.Valid || text.String == "" {
			if hasAttributedBody {
				skipped++
			}
			continue
		}

		candidates = append(candidates, corpus.Candidate{
			ID:        "imessage-" + strconv.FormatInt(rowID, 10),
			Source:    "imessage",
			Author:    corpus.AuthorMe,
			Context:   chatIdentifier,
			Timestamp: macAbsoluteTimeToTime(date),
			Text:      text.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating message rows: %w", err)
	}

	return candidates, skipped, nil
}

// macAbsoluteTimeToTime converts message.date to time.Time. Since macOS
// 10.13 (High Sierra), chat.db stores nanoseconds since the mac epoch; older
// databases store whole seconds. A nanosecond value for any date since ~2011
// is far larger than a plausible seconds value, so magnitude disambiguates
// the two formats.
func macAbsoluteTimeToTime(v int64) time.Time {
	const nanosecondThreshold = 1_000_000_000_000 // ~31,700 years in seconds; real second values never reach this
	if v > nanosecondThreshold {
		return macEpoch.Add(time.Duration(v) * time.Nanosecond)
	}
	return macEpoch.Add(time.Duration(v) * time.Second)
}
