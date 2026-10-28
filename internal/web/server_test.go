package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"hearth/internal/config"
	"hearth/internal/store"
	"hearth/internal/web"
)

func TestRegisterRejectsShortPassword(t *testing.T) {
	ts, _ := newServer(t)
	b := newBrowser(t, ts.URL)
	status, body, _ := b.get("/register")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	status, body, _ = b.post("/register", url.Values{
		"_csrf":    {csrf(t, body)},
		"name":     {"Ana"},
		"email":    {"ana@example.com"},
		"password": {"short"},
	})
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "at least 10") {
		t.Fatalf("status %d body %s", status, body)
	}
}

func TestHouseholdBillFlow(t *testing.T) {
	ts, st := newServer(t)
	ana := newBrowser(t, ts.URL)
	ana.register("Ana", "ana@example.com")
	_, home := ana.followPath("/")
	ana.submit("/households", url.Values{
		"_csrf":    {csrf(t, home)},
		"name":     {"Home"},
		"currency": {"USD"},
	}, "Household created.")

	status, body, _ := ana.get("/utilities")
	if status != http.StatusOK {
		t.Fatal(status)
	}
	ana.submit("/utilities", url.Values{
		"_csrf":    {csrf(t, body)},
		"name":     {"Electricity"},
		"provider": {"City Power"},
		"category": {"electric"},
	}, "Utility added.")

	luis := newBrowser(t, ts.URL)
	luis.register("Luis", "luis@example.com")

	status, body, _ = ana.get("/members")
	ana.submit("/members/invite", url.Values{
		"_csrf": {csrf(t, body)},
		"email": {"luis@example.com"},
	}, "Invite created")
	invite := cookieValue(t, ana, "hearth_invite")

	status, body, _ = luis.get("/invites/" + invite)
	if !strings.Contains(body, "Join Home") {
		t.Fatalf("invite page:\n%s", body)
	}
	luis.submit("/invites/"+invite+"/accept", url.Values{"_csrf": {csrf(t, body)}}, "joined the household")

	status, body, _ = ana.get("/members")
	if status != http.StatusOK || !strings.Contains(body, "Luis") {
		t.Fatalf("members %d\n%s", status, body)
	}

	ctx := context.Background()
	owner, err := st.UserByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	homes, err := st.ListHouseholds(ctx, owner.ID)
	if err != nil || len(homes) != 1 {
		t.Fatal(err, len(homes))
	}
	utils, err := st.Utilities(ctx, homes[0].ID)
	if err != nil || len(utils) != 1 {
		t.Fatal(err, len(utils))
	}
	debtor, err := st.UserByEmail(ctx, "luis@example.com")
	if err != nil {
		t.Fatal(err)
	}
	members, err := st.Members(ctx, homes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var anaPart, luisPart string
	for _, m := range members {
		if m.UserID == owner.ID {
			anaPart = m.ParticipantID
		}
		if m.UserID == debtor.ID {
			luisPart = m.ParticipantID
		}
	}
	if anaPart == "" || luisPart == "" {
		t.Fatalf("participants ana=%s luis=%s", anaPart, luisPart)
	}

	status, body, _ = ana.get("/bills/new")
	ana.submit("/bills", url.Values{
		"_csrf":        {csrf(t, body)},
		"utility_id":   {utils[0].ID},
		"period_start": {"2026-10-01"},
		"period_end":   {"2026-10-31"},
		"due_date":     {"2026-11-10"},
		"amount":       {"100.00"},
		"paid_by":      {anaPart},
		"split_method": {"equal"},
	}, "Bill recorded.")

	_, home = ana.followPath("/")
	if !strings.Contains(home, "Luis owes Ana") || !strings.Contains(home, "$50.00") {
		t.Fatalf("ledger:\n%s", home)
	}

	bills, err := st.Bills(ctx, homes[0].ID)
	if err != nil || len(bills) != 1 {
		t.Fatal(err, len(bills))
	}
	_, list := luis.followPath("/bills")
	if !strings.Contains(list, "You owe $50.00") {
		t.Fatalf("luis bills:\n%s", list)
	}
	_, billPage := luis.followPath("/bills/" + bills[0].ID)
	luis.submit("/bills/"+bills[0].ID+"/paid", url.Values{
		"_csrf":          {csrf(t, billPage)},
		"participant_id": {luisPart},
		"paid":           {"1"},
	}, "marked as paid")

	_, settled := ana.followPath("/")
	if !strings.Contains(settled, "Everyone is square.") || strings.Contains(settled, "Luis owes Ana") {
		t.Fatalf("settled ledger:\n%s", settled)
	}
	_, finalBill := ana.followPath("/bills/" + bills[0].ID)
	if !strings.Contains(finalBill, "Settled") {
		t.Fatalf("final bill:\n%s", finalBill)
	}
}

func TestGuestIsExemptFromUtility(t *testing.T) {
	ts, st := newServer(t)
	ana := newBrowser(t, ts.URL)
	ana.register("Ana", "ana@example.com")
	_, home := ana.followPath("/")
	ana.submit("/households", url.Values{
		"_csrf":    {csrf(t, home)},
		"name":     {"Home"},
		"currency": {"USD"},
	}, "Household created.")

	ctx := context.Background()
	owner, err := st.UserByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	homes, err := st.ListHouseholds(ctx, owner.ID)
	if err != nil || len(homes) != 1 {
		t.Fatal(err)
	}
	members, err := st.Members(ctx, homes[0].ID)
	if err != nil || len(members) != 1 {
		t.Fatal(err, len(members))
	}

	_, page := ana.followPath("/members")
	ana.submit("/members/participants", url.Values{
		"_csrf": {csrf(t, page)},
		"name":  {"Kid"},
	}, "without an account")
	members, err = st.Members(ctx, homes[0].ID)
	if err != nil || len(members) != 2 {
		t.Fatal(err, len(members))
	}
	var anaPart, kid string
	for _, m := range members {
		if m.Name == "Ana" {
			anaPart = m.ParticipantID
		}
		if m.Name == "Kid" {
			kid = m.ParticipantID
		}
	}
	_, page = ana.followPath("/utilities")
	ana.submit("/utilities", url.Values{
		"_csrf":              {csrf(t, page)},
		"name":               {"Internet"},
		"category":           {"internet"},
		"includes_sent":      {"1"},
		"include_" + anaPart: {"1"},
	}, "Utility added.")
	utils, err := st.Utilities(ctx, homes[0].ID)
	if err != nil || len(utils) != 1 {
		t.Fatal(err)
	}
	status, body, _ := ana.get("/bills/split-fields?utility_id=" + utils[0].ID + "&amount=10.00&split_method=equal")
	if status != http.StatusOK || !strings.Contains(body, "Ana") || strings.Contains(body, ">Kid<") {
		t.Fatalf("split preview %d\n%s", status, body)
	}
	if strings.Contains(body, "include_"+kid) && strings.Contains(body, `name="include_`+kid+`" value="1" checked`) {
		t.Fatalf("kid should start unchecked\n%s", body)
	}
}

type browser struct {
	t      *testing.T
	base   string
	client *http.Client
}

func newServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hearth.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv, err := web.New(config.Config{Env: "test", DataPath: dbPath}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Routes()), st
}

func newBrowser(t *testing.T, base string) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &browser{
		t:    t,
		base: base,
		client: &http.Client{
			Jar:     jar,
			Timeout: 0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (b *browser) register(name, email string) {
	b.t.Helper()
	status, body, _ := b.get("/register")
	if status != http.StatusOK {
		b.t.Fatalf("register page %d", status)
	}
	b.submit("/register", url.Values{
		"_csrf":         {csrf(b.t, body)},
		"state":         {inputValue(b.t, body, "state")},
		"client_id":     {inputValue(b.t, body, "client_id")},
		"redirect_uri":  {inputValue(b.t, body, "redirect_uri")},
		"response_type": {"code"},
		"name":          {name},
		"email":         {email},
		"password":      {"correct-horse"},
	}, "")
}

func (b *browser) submit(path string, form url.Values, want string) {
	b.t.Helper()
	if form.Get("_csrf") == "" {
		_, page, _ := b.get(path)
		form.Set("_csrf", csrf(b.t, page))
	}
	status, body, header := b.post(path, form)
	if status != http.StatusSeeOther && status != http.StatusFound {
		b.t.Fatalf("POST %s status %d\n%s", path, status, body)
	}
	_, landed := b.follow(header)
	if want != "" && !strings.Contains(landed, want) {
		b.t.Fatalf("POST %s landing missing %q\n%s", path, want, landed)
	}
}

func (b *browser) followPath(path string) (int, string) {
	b.t.Helper()
	status, body, _ := b.get(path)
	if status != http.StatusOK {
		b.t.Fatalf("GET %s status %d\n%s", path, status, body)
	}
	return status, body
}

func (b *browser) get(path string) (int, string, http.Header) {
	return b.send(http.MethodGet, path, nil)
}

func (b *browser) post(path string, form url.Values) (int, string, http.Header) {
	return b.send(http.MethodPost, path, form)
}

func (b *browser) send(method, path string, form url.Values) (int, string, http.Header) {
	b.t.Helper()
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = b.base + path
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		b.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp.StatusCode, string(payload), resp.Header
}

func (b *browser) follow(header http.Header) (int, string) {
	b.t.Helper()
	loc := header.Get("Location")
	var status int
	var body string
	for i := 0; i < 6; i++ {
		if loc == "" {
			b.t.Fatal("missing redirect")
		}
		if strings.HasPrefix(loc, b.base) {
			loc = strings.TrimPrefix(loc, b.base)
		}
		var hdr http.Header
		status, body, hdr = b.get(loc)
		if status == http.StatusOK {
			return status, body
		}
		if status != http.StatusSeeOther && status != http.StatusFound {
			b.t.Fatalf("redirect %s status %d\n%s", loc, status, body)
		}
		loc = hdr.Get("Location")
	}
	b.t.Fatalf("too many redirects, last %d\n%s", status, body)
	return status, body
}

func inputValue(t *testing.T, body, name string) string {
	t.Helper()
	key := `name="` + name + `" value="`
	i := strings.Index(body, key)
	if i < 0 {
		t.Fatalf("input %s missing\n%s", name, body)
	}
	rest := body[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatal("input truncated")
	}
	return rest[:j]
}

func csrf(t *testing.T, body string) string {
	t.Helper()
	return inputValue(t, body, "_csrf")
}

func cookieValue(t *testing.T, b *browser, name string) string {
	t.Helper()
	u, err := url.Parse(b.base)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range b.client.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("cookie %s missing", name)
	return ""
}
