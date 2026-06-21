CREATE TABLE IF NOT EXISTS downloads (
	id            TEXT PRIMARY KEY,
	url           TEXT NOT NULL,
	destination   TEXT NOT NULL,
	total_size    INTEGER NOT NULL,
	status        TEXT NOT NULL,
	etag          TEXT NOT NULL,
	last_modified TEXT NOT NULL,
	checksum      TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS segments (
	download_id TEXT NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
	idx         INTEGER NOT NULL,
	"start"     INTEGER NOT NULL,
	"end"       INTEGER NOT NULL,
	completed   INTEGER NOT NULL,
	PRIMARY KEY (download_id, idx)
);

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
