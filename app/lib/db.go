package lib

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDB(dbPath string) {
	var err error
	DB, err = sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}

	if err = DB.Ping(); err != nil {
		log.Fatalf("ping sqlite: %v", err)
	}

	DB.SetMaxOpenConns(1)

	migrate()

	// Periodically checkpoint the WAL so it doesn't balloon between restarts.
	// PASSIVE mode never blocks readers or writers.
	go func() {
		for range time.Tick(5 * time.Minute) {
			if _, err := DB.Exec("PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
				log.Printf("wal_checkpoint: %v", err)
			}
		}
	}()

	log.Println("SQLite initialized:", dbPath)
}

func migrate() {
	_, err := DB.Exec(`
		CREATE TABLE IF NOT EXISTS drawings (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT 'Untitled',
			content TEXT,
			share_slug TEXT UNIQUE,
			thumbnail TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_drawings_owner ON drawings(owner_id);
		CREATE INDEX IF NOT EXISTS idx_drawings_share_slug ON drawings(share_slug);

		-- Thumbnails live in their own table so list queries don't
		-- drag up to 100KB of base64 PNG per row on the main drawings table.
		CREATE TABLE IF NOT EXISTS drawing_thumbnails (
			drawing_id TEXT PRIMARY KEY REFERENCES drawings(id) ON DELETE CASCADE,
			data       TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		-- VIP whitelist for closed-beta multiplayer access.
		CREATE TABLE IF NOT EXISTS feature_whitelist (
			email      TEXT PRIMARY KEY,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// One-time idempotent migration: move existing thumbnail blobs out of the
	// drawings table into drawing_thumbnails. Safe to run on every startup —
	// INSERT OR IGNORE is a no-op for rows already migrated.
	_, err = DB.Exec(`
		INSERT OR IGNORE INTO drawing_thumbnails (drawing_id, data, updated_at)
			SELECT id, thumbnail, updated_at FROM drawings
			WHERE thumbnail IS NOT NULL AND thumbnail != '';
		UPDATE drawings SET thumbnail = NULL
			WHERE thumbnail IS NOT NULL AND thumbnail != '';
	`)
	if err != nil {
		log.Fatalf("thumbnail migration: %v", err)
	}

	// Idempotent: add allow_public_edits column for multiplayer opt-in.
	// SQLite does not support IF NOT EXISTS on ALTER TABLE, so we check manually.
	var colCount int
	_ = DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('drawings') WHERE name='allow_public_edits'`).Scan(&colCount)
	if colCount == 0 {
		if _, err = DB.Exec(`ALTER TABLE drawings ADD COLUMN allow_public_edits INTEGER NOT NULL DEFAULT 0`); err != nil {
			log.Fatalf("migrate allow_public_edits: %v", err)
		}
	}
}
