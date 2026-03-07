package store

import (
	"database/sql"

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
	// Drop old tables to rebuild schema (dev only, no production data)
	drops := []string{
		"DROP TABLE IF EXISTS worker_memories",
		"DROP TABLE IF EXISTS emails",
		"DROP TABLE IF EXISTS task_executions",
		"DROP TABLE IF EXISTS tasks",
		"DROP TABLE IF EXISTS worker_executions",
		"DROP TABLE IF EXISTS workers",
	}
	for _, drop := range drops {
		if _, err := db.Exec(drop); err != nil {
			return err
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS workers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL UNIQUE,
		runtime_type TEXT NOT NULL DEFAULT 'claude_code',
		work_dir TEXT NOT NULL,
		trigger_type TEXT NOT NULL DEFAULT 'message',
		cron_expression TEXT NOT NULL DEFAULT '',
		recipients TEXT NOT NULL DEFAULT '[]',
		requires_approval INTEGER NOT NULL DEFAULT 0,
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
		ai_process_pid INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME,
		completed_at DATETIME,
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS emails (
		id TEXT PRIMARY KEY,
		execution_id TEXT NOT NULL,
		from_addr TEXT NOT NULL,
		to_addr TEXT NOT NULL,
		cc_addr TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL,
		body TEXT NOT NULL,
		in_reply_to TEXT NOT NULL DEFAULT '',
		direction TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (execution_id) REFERENCES worker_executions(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS worker_memories (
		id TEXT PRIMARY KEY,
		worker_id TEXT NOT NULL,
		execution_id TEXT NOT NULL,
		summary TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE,
		FOREIGN KEY (execution_id) REFERENCES worker_executions(id) ON DELETE CASCADE
	);
	`
	_, err := db.Exec(schema)
	return err
}
