package control

type migration struct {
	version    int
	statements []string
}

// Version 8 is idempotent so it initializes new databases and upgrades any
// database created by the removed grant/operation architecture.
var migrations = []migration{{
	version: 8,
	statements: []string{
		`CREATE TABLE IF NOT EXISTS profiles (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			profile_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			sdk_dedup_key TEXT NOT NULL,
			type TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			occurred_at TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			UNIQUE(profile_id, sequence),
			UNIQUE(profile_id, sdk_dedup_key)
		)`,
		`CREATE INDEX IF NOT EXISTS events_profile_recorded_at ON events(profile_id, recorded_at)`,
		`CREATE TABLE IF NOT EXISTS provider_sessions (
			profile_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			session_ref TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(profile_id, conversation_id, provider)
		)`,
	},
}}
