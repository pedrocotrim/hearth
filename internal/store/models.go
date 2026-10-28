package store

import "time"

type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

type Access struct {
	UserID      string
	HouseholdID string
	RefreshRaw  string
}

type Household struct {
	ID        string
	Name      string
	Currency  string
	CreatedAt time.Time
}

type Membership struct {
	HouseholdID string
	UserID      string
	Role        string
	CreatedAt   time.Time
}

type Member struct {
	ParticipantID string
	UserID        string
	Name          string
	Email         string
	Role          string
	Joined        time.Time
}

type Invite struct {
	ID            string
	HouseholdID   string
	HouseholdName string
	Email         string
	ExpiresAt     time.Time
	AcceptedAt    *time.Time
	CreatedAt     time.Time
}

type Utility struct {
	ID          string
	HouseholdID string
	Name        string
	Provider    string
	Category    string
	CreatedAt   time.Time
}

type Bill struct {
	ID                  string
	HouseholdID         string
	UtilityID           string
	UtilityName         string
	Provider            string
	Category            string
	PeriodStart         time.Time
	PeriodEnd           time.Time
	DueDate             time.Time
	AmountCents         int64
	PaidByParticipantID string
	PaidByName          string
	InvoiceRef          string
	Notes               string
	SplitMethod         string
	CreatedBy           string
	CreatedAt           time.Time
	Shares              []Share
}

type Share struct {
	ParticipantID string
	Name          string
	AmountCents   int64
	Weight        int
	PaidCents     int64
}

type NewBill struct {
	HouseholdID         string
	UtilityID           string
	PeriodStart         time.Time
	PeriodEnd           time.Time
	DueDate             time.Time
	AmountCents         int64
	PaidByParticipantID string
	InvoiceRef          string
	Notes               string
	SplitMethod         string
	CreatedBy           string
	Shares              []NewShare
}

type NewShare struct {
	ParticipantID string
	AmountCents   int64
	Weight        int
}
