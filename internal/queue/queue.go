// Package queue is the persistent, deduplicated work queue backing tidlr. Each
// item is an album keyed by ADM ReviewID and moves through states as the
// pipeline processes it. Backed by SQLite (pure-Go, no cgo).
package queue

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/simonb/tidlr/internal/adm"
)

// State is an item's position in the pipeline.
type State string

const (
	StatePending     State = "pending"     // enqueued, not yet started
	StateDownloading State = "downloading" // tiddl fetch in progress
	StateConverting  State = "converting"  // FLAC->ALAC in progress
	StateDone        State = "done"        // ALAC delivered
	StateFailed      State = "failed"      // gave up after error
	// StateSkipped means no Tidal album matches the ADM title (typically
	// because ADM's title is garbled, or Tidal lacks the album). Unlike
	// StateFailed, RequeueFailed leaves these alone, so they stop churning on
	// every `retry`; RequeueSkipped tries them again on request, for when
	// Tidal's catalogue or the matcher has changed.
	StateSkipped State = "skipped"
	// StateReview means the album downloaded but could not be identified or
	// filed in the library; it waits in the review folder for a person.
	// Permanent like StateSkipped: retrying would only download it again.
	StateReview State = "review"
)

// Item is one album in the queue.
type Item struct {
	ReviewID  int
	Artist    string
	Album     string
	ReviewURL string
	State     State
	Attempts  int
	LastError string
	FLACDir   string // scratch dir where FLACs landed, once downloaded
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Queue is a handle to the persistent store.
type Queue struct {
	db *sql.DB
}

// Open opens (creating if needed) the queue database at path.
func Open(path string) (*Queue, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	// SQLite is single-writer; serialize to avoid "database is locked".
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting pragmas: %w", err)
	}
	q := &Queue{db: db}
	if err := q.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return q, nil
}

// Close releases the database.
func (q *Queue) Close() error { return q.db.Close() }

func (q *Queue) migrate() error {
	_, err := q.db.Exec(`
		CREATE TABLE IF NOT EXISTS items (
			review_id  INTEGER PRIMARY KEY,
			artist     TEXT NOT NULL,
			album      TEXT NOT NULL,
			review_url TEXT NOT NULL,
			state      TEXT NOT NULL,
			attempts   INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			flac_dir   TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_items_state ON items(state);

		-- Permanent record of albums we have successfully downloaded. This is
		-- the source of truth for "do we already have it", independent of the
		-- transient items work-queue (which may be cleared between runs).
		CREATE TABLE IF NOT EXISTS downloads (
			review_id      INTEGER PRIMARY KEY,
			artist         TEXT NOT NULL,
			album          TEXT NOT NULL,
			output_dir     TEXT NOT NULL DEFAULT '',
			downloaded_at  TIMESTAMP NOT NULL
		);

		-- Permanent record of albums fetched directly by Tidal album id
		-- (tidlr --album). Kept separate from the downloads table because that is
		-- keyed by ADM review id: the two id spaces are unrelated and sharing a
		-- primary key would let an ADM review id collide with a Tidal album id.
		-- Lookups worth keeping between runs (e.g. Last.fm tags), by
		-- namespace and key.
		CREATE TABLE IF NOT EXISTS cache (
			ns         TEXT NOT NULL,
			key        TEXT NOT NULL,
			value      BLOB NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (ns, key)
		);

		CREATE TABLE IF NOT EXISTS tidal_downloads (
			album_id       INTEGER PRIMARY KEY,
			artist         TEXT NOT NULL,
			album          TEXT NOT NULL,
			output_dir     TEXT NOT NULL DEFAULT '',
			downloaded_at  TIMESTAMP NOT NULL
		);
	`)
	return err
}

// Cache is a persistent key-value store in one namespace of the database.
type Cache struct {
	q  *Queue
	ns string
}

// Cache returns the store for namespace ns.
func (q *Queue) Cache(ns string) Cache { return Cache{q, ns} }

// Get returns the value stored under key.
func (c Cache) Get(key string) ([]byte, bool) {
	var v []byte
	err := c.q.db.QueryRow(`SELECT value FROM cache WHERE ns = ? AND key = ?`, c.ns, key).Scan(&v)
	return v, err == nil
}

// Put stores value under key, replacing any previous value. Failures are
// ignored: a cache that can't be written only costs a later lookup.
func (c Cache) Put(key string, value []byte) {
	c.q.db.Exec(`INSERT INTO cache (ns, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(ns, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		c.ns, key, value, time.Now().UTC())
}

// IsDownloaded reports whether an album (by ADM ReviewID) is already recorded
// as successfully downloaded.
func (q *Queue) IsDownloaded(reviewID int) (bool, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM downloads WHERE review_id = ?`, reviewID).Scan(&n)
	return n > 0, err
}

// DownloadedIDs returns the set of all ReviewIDs already downloaded.
func (q *Queue) DownloadedIDs() (map[int]bool, error) {
	rows, err := q.db.Query(`SELECT review_id FROM downloads`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// MarkDownloaded records an album as permanently downloaded. Idempotent: a
// re-download (e.g. via --force) refreshes the row.
func (q *Queue) MarkDownloaded(reviewID int, artist, album, outputDir string) error {
	_, err := q.db.Exec(`
		INSERT INTO downloads (review_id, artist, album, output_dir, downloaded_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(review_id) DO UPDATE SET
			artist=excluded.artist, album=excluded.album,
			output_dir=excluded.output_dir, downloaded_at=excluded.downloaded_at`,
		reviewID, artist, album, outputDir, time.Now().UTC())
	return err
}

// IsTidalDownloaded reports whether a Tidal album id has already been fetched
// via --album.
func (q *Queue) IsTidalDownloaded(albumID int64) (bool, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM tidal_downloads WHERE album_id = ?`, albumID).Scan(&n)
	return n > 0, err
}

// TidalDownload returns the recorded artist/album/output dir for an already
// downloaded Tidal album id, so callers can say what they are skipping. ok is
// false when the id has not been downloaded.
func (q *Queue) TidalDownload(albumID int64) (artist, album, outputDir string, ok bool, err error) {
	row := q.db.QueryRow(
		`SELECT artist, album, output_dir FROM tidal_downloads WHERE album_id = ?`, albumID)
	switch err = row.Scan(&artist, &album, &outputDir); {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", "", false, nil
	case err != nil:
		return "", "", "", false, err
	}
	return artist, album, outputDir, true, nil
}

// MarkTidalDownloaded records a directly-fetched Tidal album. Idempotent: a
// re-download (e.g. via --force) refreshes the row.
func (q *Queue) MarkTidalDownloaded(albumID int64, artist, album, outputDir string) error {
	_, err := q.db.Exec(`
		INSERT INTO tidal_downloads (album_id, artist, album, output_dir, downloaded_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(album_id) DO UPDATE SET
			artist=excluded.artist, album=excluded.album,
			output_dir=excluded.output_dir, downloaded_at=excluded.downloaded_at`,
		albumID, artist, album, outputDir, time.Now().UTC())
	return err
}

// Enqueue inserts releases that aren't already present. Returns how many were
// newly added. Existing queue rows are left untouched (idempotent dedupe).
//
// Unless force is true, releases already recorded in the permanent downloads
// library are skipped so we don't re-fetch what we already have. With force,
// an already-downloaded release is re-enqueued as pending (its queue row is
// reset), causing a re-download that overwrites the existing files.
func (q *Queue) Enqueue(releases []adm.Release, force bool) (int, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	insert, err := tx.Prepare(`
		INSERT INTO items (review_id, artist, album, review_url, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(review_id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer insert.Close()

	// For --force we reset any existing queue row back to pending so it runs again.
	reset, err := tx.Prepare(`UPDATE items SET state=?, last_error='', flac_dir='', updated_at=? WHERE review_id=?`)
	if err != nil {
		return 0, err
	}
	defer reset.Close()

	now := time.Now().UTC()
	added := 0
	for _, r := range releases {
		if !force {
			var have int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM downloads WHERE review_id=?`, r.ReviewID).Scan(&have); err != nil {
				return added, err
			}
			if have > 0 {
				continue // already in the library; skip
			}
		}
		res, err := insert.Exec(r.ReviewID, r.Artist, r.Album, r.ReviewURL, StatePending, now, now)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		} else if force {
			// Row already existed; force it back to pending to re-run.
			if _, err := reset.Exec(StatePending, now, r.ReviewID); err != nil {
				return added, err
			}
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// ClaimPending atomically transitions the oldest pending item to the given
// working state and returns it. Returns (nil, nil) when nothing is pending.
// This lets multiple workers pull distinct items without double-processing.
func (q *Queue) ClaimPending(working State) (*Item, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`
		SELECT review_id, artist, album, review_url, state, attempts, last_error, flac_dir, created_at, updated_at
		FROM items WHERE state = ? ORDER BY created_at ASC LIMIT 1`, StatePending)

	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(`UPDATE items SET state=?, attempts=attempts+1, updated_at=? WHERE review_id=?`,
		working, now, it.ReviewID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	it.State = working
	it.Attempts++
	return it, nil
}

// SetState updates an item's state (and clears any prior error).
func (q *Queue) SetState(reviewID int, state State) error {
	_, err := q.db.Exec(`UPDATE items SET state=?, last_error='', updated_at=? WHERE review_id=?`,
		state, time.Now().UTC(), reviewID)
	return err
}

// SetFLACDir records where an item's FLACs were downloaded.
func (q *Queue) SetFLACDir(reviewID int, dir string) error {
	_, err := q.db.Exec(`UPDATE items SET flac_dir=?, updated_at=? WHERE review_id=?`,
		dir, time.Now().UTC(), reviewID)
	return err
}

// Fail marks an item failed and records the error message.
func (q *Queue) Fail(reviewID int, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	_, err := q.db.Exec(`UPDATE items SET state=?, last_error=?, updated_at=? WHERE review_id=?`,
		StateFailed, msg, time.Now().UTC(), reviewID)
	return err
}

// Skip marks an item permanently skipped: a retry cannot help, so RequeueFailed
// will not pick it up again. Used when no Tidal album matches the ADM title.
func (q *Queue) Skip(reviewID int, cause error) error {
	return q.finish(reviewID, StateSkipped, cause)
}

// Review marks an item held for review: downloaded, but not identified or
// filed in the library. Like Skip it is permanent, recording why.
func (q *Queue) Review(reviewID int, cause error) error {
	return q.finish(reviewID, StateReview, cause)
}

// finish moves an item to a terminal state, recording the cause.
func (q *Queue) finish(reviewID int, state State, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	_, err := q.db.Exec(`UPDATE items SET state=?, last_error=?, updated_at=? WHERE review_id=?`,
		state, msg, time.Now().UTC(), reviewID)
	return err
}

// InState returns the items in a state, oldest first.
func (q *Queue) InState(state State) ([]Item, error) {
	rows, err := q.db.Query(`
		SELECT review_id, artist, album, review_url, state, attempts, last_error, flac_dir, created_at, updated_at
		FROM items WHERE state = ? ORDER BY updated_at`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// RequeueFailed moves all failed items back to pending so a subsequent run
// retries them. Skipped items are deliberately excluded — they are permanent.
// Returns how many were requeued.
func (q *Queue) RequeueFailed() (int, error) { return q.requeue(StateFailed) }

// RequeueSkipped moves all skipped items back to pending, so a run looks for
// them on Tidal again. Returns how many were requeued.
func (q *Queue) RequeueSkipped() (int, error) { return q.requeue(StateSkipped) }

func (q *Queue) requeue(state State) (int, error) {
	res, err := q.db.Exec(`UPDATE items SET state=?, last_error='', updated_at=? WHERE state=?`,
		StatePending, time.Now().UTC(), state)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RequeueStale moves items stuck mid-flight (downloading/converting) back to
// pending. Use at startup to recover from a crash or hard kill that left items
// claimed but not finished. Returns how many were reset.
func (q *Queue) RequeueStale() (int, error) {
	res, err := q.db.Exec(`UPDATE items SET state=?, updated_at=? WHERE state IN (?, ?)`,
		StatePending, time.Now().UTC(), StateDownloading, StateConverting)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Counts returns the number of items in each state.
func (q *Queue) Counts() (map[State]int, error) {
	rows, err := q.db.Query(`SELECT state, COUNT(*) FROM items GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[State]int{}
	for rows.Next() {
		var s State
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanItem(s scanner) (*Item, error) {
	var it Item
	err := s.Scan(&it.ReviewID, &it.Artist, &it.Album, &it.ReviewURL, &it.State,
		&it.Attempts, &it.LastError, &it.FLACDir, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &it, nil
}

// Activity is what happened to one album: filed, held for review, not
// found on Tidal, or failed.
type Activity struct {
	Artist, Album string
	State         State  // StateDone, StateReview, StateSkipped or StateFailed
	Detail        string // the reason, for anything but StateDone
	Dir           string // where it is now, when known
	When          time.Time
}

// ActivitySince lists albums that reached an outcome at or after since,
// oldest first: queue items (AnyDecentMusic and chart albums) and albums
// downloaded directly with --album.
func (q *Queue) ActivitySince(since time.Time) ([]Activity, error) {
	// Timestamps are stored as UTC text, which sorts as time does.
	from := since.UTC().Format("2006-01-02 15:04:05")
	rows, err := q.db.Query(`
		SELECT i.artist, i.album, i.state, i.last_error, COALESCE(d.output_dir, ''), i.updated_at
		FROM items i LEFT JOIN downloads d USING (review_id)
		WHERE i.updated_at >= ? AND i.state IN (?, ?, ?, ?)
		UNION ALL
		SELECT artist, album, ?, '', output_dir, downloaded_at
		FROM tidal_downloads WHERE downloaded_at >= ?
		ORDER BY 6`,
		from, StateDone, StateReview, StateSkipped, StateFailed, StateDone, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activity
	seen := map[string]bool{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.Artist, &a.Album, &a.State, &a.Detail, &a.Dir, &a.When); err != nil {
			return nil, err
		}
		// A chart album may be recorded both ways; list it once.
		if a.Dir != "" && a.State == StateDone {
			if seen[a.Dir] {
				continue
			}
			seen[a.Dir] = true
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
