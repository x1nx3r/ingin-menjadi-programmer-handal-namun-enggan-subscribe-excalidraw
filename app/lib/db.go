package lib

import (
	"database/sql"
	"log"

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
	`)
	if err != nil {
		log.Fatalf("migrate: %v", err)
	}
}
