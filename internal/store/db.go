package store

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

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
		schedule_description TEXT NOT NULL DEFAULT '',
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

	CREATE TABLE IF NOT EXISTS platform_sessions (
		session_key TEXT NOT NULL,
		platform TEXT NOT NULL,
		worker_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		last_execution_id TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (session_key, platform)
	);

	CREATE INDEX IF NOT EXISTS idx_worker_executions_worker_id ON worker_executions(worker_id);
	CREATE INDEX IF NOT EXISTS idx_worker_executions_session_id ON worker_executions(session_id);
	CREATE INDEX IF NOT EXISTS idx_workers_schedule ON workers(schedule_enabled) WHERE schedule_enabled = 1;
	`
	_, err := db.Exec(schema)
	return err
}
