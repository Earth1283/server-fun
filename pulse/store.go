package main

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists every sample to a SQLite file so runs can be replayed or
// analysed later (and fed to the manager dashboard).
type Store struct {
	db   *sql.DB
	run  int64
	stmt *sql.Stmt
}

func openStore(path, target string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Pragmas: WAL keeps writes cheap during a live monitor.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;`); err != nil {
		return nil, err
	}
	schema := `
	CREATE TABLE IF NOT EXISTS runs (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		target  TEXT NOT NULL,
		started INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS samples (
		run      INTEGER NOT NULL,
		ts       INTEGER NOT NULL,
		ok       INTEGER NOT NULL,
		latency  REAL,
		online   INTEGER,
		max      INTEGER,
		version  TEXT,
		protocol INTEGER,
		motd     TEXT,
		err      TEXT,
		FOREIGN KEY(run) REFERENCES runs(id)
	);
	CREATE INDEX IF NOT EXISTS idx_samples_run_ts ON samples(run, ts);`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	res, err := db.Exec(`INSERT INTO runs(target, started) VALUES(?, ?)`, target, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	run, _ := res.LastInsertId()

	stmt, err := db.Prepare(`INSERT INTO samples
		(run, ts, ok, latency, online, max, version, protocol, motd, err)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, run: run, stmt: stmt}, nil
}

func (s *Store) save(smp sample) error {
	if s == nil {
		return nil
	}
	ok := 0
	if smp.ok {
		ok = 1
	}
	_, err := s.stmt.Exec(s.run, smp.t.UnixMilli(), ok, smp.latency,
		smp.online, smp.max, smp.version, smp.protocol, smp.motd, smp.errMsg)
	return err
}

func (s *Store) close() {
	if s == nil {
		return
	}
	s.stmt.Close()
	s.db.Close()
}
