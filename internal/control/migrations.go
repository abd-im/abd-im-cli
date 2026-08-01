package control

type migration struct {
	version    int
	statements []string
}

var migrations = []migration{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE profiles (
				id TEXT PRIMARY KEY,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE TABLE events (
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
			`CREATE TABLE operations (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				scope TEXT NOT NULL,
				idempotency_key TEXT NOT NULL,
				input_digest TEXT NOT NULL,
				status TEXT NOT NULL CHECK(status IN ('confirmed', 'failed', 'unknown')),
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(profile_id, scope, idempotency_key)
			)`,
			`CREATE TABLE grants (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				run_id TEXT NOT NULL,
				principal TEXT NOT NULL,
				scopes TEXT NOT NULL,
				target_allowlist TEXT NOT NULL,
				message_window TEXT NOT NULL,
				attachment_byte_limit INTEGER NOT NULL,
				rate_limit INTEGER NOT NULL,
				approval_policy TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL
			)`,
			`CREATE INDEX events_profile_recorded_at ON events(profile_id, recorded_at)`,
			`CREATE INDEX operations_profile_created_at ON operations(profile_id, created_at)`,
			`CREATE INDEX grants_run_id ON grants(run_id)`,
		},
	},
	{
		version: 2,
		statements: []string{
			`CREATE TABLE reply_slots (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				event_id TEXT NOT NULL,
				conversation_id TEXT NOT NULL,
				trigger_message_id TEXT NOT NULL,
				run_id TEXT NOT NULL,
				operation_id TEXT NOT NULL,
				created_at TEXT NOT NULL,
				UNIQUE(profile_id, event_id)
			)`,
			`CREATE INDEX reply_slots_operation_id ON reply_slots(operation_id)`,
		},
	},
	{
		version: 3,
		statements: []string{
			`ALTER TABLE reply_slots ADD COLUMN recipient_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE reply_slots ADD COLUMN group_id TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		version: 4,
		statements: []string{
			`CREATE TABLE attachments (
				id TEXT PRIMARY KEY,
				profile_id TEXT NOT NULL,
				run_id TEXT NOT NULL,
				grant_id TEXT NOT NULL,
				kind TEXT NOT NULL,
				size_bytes INTEGER NOT NULL,
				byte_limit INTEGER NOT NULL,
				expires_at TEXT NOT NULL,
				created_at TEXT NOT NULL
			)`,
			`CREATE INDEX attachments_profile_run ON attachments(profile_id, run_id, grant_id)`,
		},
	},
}
