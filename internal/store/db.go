package store

import (
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS workers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		work_dir TEXT NOT NULL,
		cron_expression TEXT NOT NULL DEFAULT '',
		schedule_enabled INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'idle',
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS worker_executions (
		id TEXT PRIMARY KEY,
		worker_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		trigger_input TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		result TEXT NOT NULL DEFAULT '',
		logs TEXT NOT NULL DEFAULT '',
		ai_process_pid INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME,
		completed_at DATETIME,
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE
	);

	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Additive migrations for existing databases
	migrations := []string{
		`ALTER TABLE worker_executions ADD COLUMN logs TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE workers ADD COLUMN schedule_description TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS feishu_sessions (
    chat_id TEXT NOT NULL,
    worker_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    last_execution_id TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chat_id)
)`,
		`CREATE TABLE IF NOT EXISTS dingtalk_sessions (
    chat_id             TEXT NOT NULL,
    worker_id           TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    last_execution_id   TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chat_id)
)`,
		`CREATE TABLE IF NOT EXISTS mail_sessions (
    thread_id           TEXT NOT NULL,
    worker_id           TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    last_execution_id   TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (thread_id)
)`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			// Ignore "duplicate column" errors from SQLite
			if !isDuplicateColumnError(err) {
				return err
			}
		}
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists")
}
