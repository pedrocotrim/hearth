package web

import (
	"html/template"

	"hearth/internal/store"
)

type View struct {
	Title   string
	Content string
	Body    template.HTML
	Active  string
	CSRF    string
	Notice  string
	Error   string
	Year    int

	UserName      string
	UserEmail     string
	IsOwner       bool
	HasHousehold  bool
	HouseholdName string
	HouseholdID   string
	Currency      string
	Households    []HouseholdOption
	DraftName     string
	DraftCurrency string
	Currencies    []string
	Categories    []Choice

	YouOwe         string
	OwedToYou      string
	AttentionCount int
	BalanceLines   []BalanceLine
	Attention      []BillRow
	ShowSetup      bool
	MemberCount    int
	HasUtilities   bool
	SettledUp      bool
	Chart          string
	ChartJSON      string
	Latest         []BillRow

	Filter   string
	BillRows []BillRow

	Utilities []UtilityOption
	People    []Person
	Form      BillForm
	Fields    map[string]string
	Bill      BillDetail

	UtilityRows []UtilityRow
	UtilityID   string
	MemberRows  []Person
	InviteRows  []InviteRow
	InviteURL   string

	InviteHousehold  string
	InviteEmail      string
	Token            string
	ClientID         string
	RedirectURI      string
	OAuthState       string
	LoggedIn         bool
	EmailMismatch    bool
	AlreadyMember    bool
	Name             string
	Email            string
	Provider         string
	SelectedCategory string
}

type HouseholdOption struct {
	ID       string
	Name     string
	Selected bool
}

type Choice struct {
	Value string
	Label string
}

type BalanceLine struct {
	Text     string
	Amount   string
	Yours    bool
	Itemized []ItemizedDebtLine
}

type ItemizedDebtLine struct {
	Text   string
	Amount string
}

type BillRow struct {
	ID          string
	Utility     string
	Category    string
	Period      string
	Due         string
	DueClass    string
	Amount      string
	Status      string
	StatusLabel string
	Payer       string
	Yours       string
}

type Person struct {
	ID          string
	Name        string
	Email       string
	Role        string
	Joined      string
	IsSelf      bool
	IsOwner     bool
	HasAccount  bool
	Included    bool
	Contributed string
	Exempt      bool
}

type UtilityOption struct {
	ID    string
	Label string
}

type UtilityRow struct {
	ID       string
	Name     string
	Provider string
	Category string
	Repeats  bool
	People   []Person
}

type InviteRow struct {
	ID      string
	Email   string
	Expires string
}

type BillForm struct {
	BillID         string
	Editing        bool
	UtilityID      string
	PeriodStart    string
	PeriodEnd      string
	DueDate        string
	Amount         string
	PaidBy         string
	InvoiceRef     string
	Notes          string
	SplitMethod    string
	IncludesSent   bool
	AppliedUtility string
	Include        map[string]bool
	Paid           map[string]bool
	Weights        map[string]string
	Amounts        map[string]string
	Preview        []SharePreview
	PreviewError   string
}

type SharePreview struct {
	Name   string
	Amount string
}

type BillDetail struct {
	ID            string
	Utility       string
	Provider      string
	Category      string
	Period        string
	Due           string
	DueClass      string
	Amount        string
	Status        string
	StatusLabel   string
	Payer         string
	AwaitingPayer bool
	InvoiceRef    string
	Notes         string
	SplitLabel    string
	Recorded      string
	CanDelete     bool
	ShowWeights   bool
	Shares        []ShareRow
}

type ShareRow struct {
	UserID    string
	Name      string
	Amount    string
	Paid      string
	Left      string
	LeftInput string
	Weight    int
	Covered   bool
	Payable   bool
	CanClear  bool
}

type scope struct {
	User       store.User
	Access     store.Access
	Household  store.Household
	Membership store.Membership
	Households []store.Household
	SelfID     string
}

func (sc scope) owner() bool {
	return sc.Membership.Role == "owner"
}
