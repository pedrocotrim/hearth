CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL COLLATE NOCASE UNIQUE,
    name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS households (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    currency TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memberships (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    created_at TEXT NOT NULL,
    PRIMARY KEY (household_id, user_id)
);

CREATE TABLE IF NOT EXISTS participants (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    email TEXT,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_participants_user ON participants(household_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_participants_email ON participants(household_id, email) WHERE email IS NOT NULL AND email != '';

CREATE TABLE IF NOT EXISTS invites (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_by TEXT NOT NULL REFERENCES users(id),
    expires_at TEXT NOT NULL,
    accepted_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS utilities (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS utility_exemptions (
    utility_id TEXT NOT NULL REFERENCES utilities(id) ON DELETE CASCADE,
    participant_id TEXT NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    PRIMARY KEY (utility_id, participant_id)
);

CREATE TABLE IF NOT EXISTS bills (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    utility_id TEXT NOT NULL REFERENCES utilities(id),
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    due_date TEXT NOT NULL,
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    paid_by_participant_id TEXT REFERENCES participants(id),
    invoice_ref TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '',
    split_method TEXT NOT NULL CHECK (split_method IN ('equal', 'weights', 'amounts')),
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS bill_shares (
    bill_id TEXT NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
    participant_id TEXT NOT NULL REFERENCES participants(id),
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    weight INTEGER NOT NULL DEFAULT 0 CHECK (weight >= 0),
    paid INTEGER NOT NULL DEFAULT 0 CHECK (paid IN (0, 1)),
    PRIMARY KEY (bill_id, participant_id)
);

CREATE TABLE IF NOT EXISTS share_payments (
    id TEXT PRIMARY KEY,
    bill_id TEXT NOT NULL,
    participant_id TEXT NOT NULL,
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    note TEXT NOT NULL DEFAULT '',
    recorded_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    FOREIGN KEY (bill_id, participant_id) REFERENCES bill_shares(bill_id, participant_id)
);

CREATE TABLE IF NOT EXISTS oauth_codes (
    code_hash TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    invite_token TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    token_hash TEXT PRIMARY KEY,
    family_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    household_id TEXT,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_memberships_user ON memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_bills_household ON bills(household_id, due_date);
CREATE INDEX IF NOT EXISTS idx_shares_participant ON bill_shares(participant_id);
CREATE INDEX IF NOT EXISTS idx_payments_bill ON share_payments(bill_id, participant_id);
CREATE INDEX IF NOT EXISTS idx_utilities_household ON utilities(household_id);
CREATE INDEX IF NOT EXISTS idx_invites_household ON invites(household_id);
CREATE INDEX IF NOT EXISTS idx_participants_household ON participants(household_id);
CREATE INDEX IF NOT EXISTS idx_refresh_family ON oauth_refresh_tokens(family_id);
