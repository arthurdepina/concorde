package signaling

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arthurdepina/concorde/internal/config"
)

// adminRequest builds a request already carrying Basic credentials.
func adminRequest(method, target, body, user, password string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.SetBasicAuth(user, password)
	return req
}

// A friend's account exists to do exactly one thing more than a visitor: open a
// room and broadcast in it.
func TestBroadcasterAccountCanOpenARoom(t *testing.T) {
	hub := testHub(config.Config{MaxParticipants: 3})
	addBroadcaster(t, hub, "ana")
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	c := dial(t, url)
	c.write(map[string]any{"type": "create-room", "user": "ana", "password": "secret-enough", "roomPassword": "pw"})
	created := c.expect("room-created")
	if created["role"] != roleBroadcaster {
		t.Fatalf("the account that opened the room should broadcast it, got %v", created["role"])
	}

	// The room remembers whose it is, so the panel can say so.
	rooms := hub.snapshotRooms()
	if len(rooms) != 1 || rooms[0].Owner != "ana" {
		t.Fatalf("expected one room owned by ana, got %+v", rooms)
	}
}

// ...and exactly nothing more: the panel stays the admin's.
func TestBroadcasterAccountIsRefusedByTheAdminPanel(t *testing.T) {
	hub := testHub(config.Config{LoginMaxFailures: 3, LoginLockoutMinutes: 15})
	addBroadcaster(t, hub, "ana")

	w := httptest.NewRecorder()
	hub.ServeAdminRooms(w, adminRequest(http.MethodGet, "/admin/rooms", "", "ana", "secret-enough"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a broadcaster account, got %d", w.Code)
	}

	// The password was right, so this is not a guess: it must not count toward
	// the lockout, or a friend refreshing the wrong page would lock themselves
	// out of opening rooms.
	if got := hub.metrics.loginLockouts.Load(); got != 0 {
		t.Fatalf("a correct password was charged to the lockout (%d)", got)
	}
	w = httptest.NewRecorder()
	hub.ServeAdminRooms(w, adminRequest(http.MethodGet, "/admin/rooms", "", "admin", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("the admin should still get in, got %d", w.Code)
	}
}

func TestAdminAccountsAPI(t *testing.T) {
	hub := testHub(config.Config{})

	// Create, with the password generated for us.
	w := httptest.NewRecorder()
	hub.ServeAdminAccounts(w, adminRequest(http.MethodPost, "/admin/accounts", `{"name":"ana"}`, "admin", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d (%s)", w.Code, w.Body)
	}
	var created struct {
		Name      string `json:"name"`
		Password  string `json:"password"`
		Generated bool   `json:"generated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Generated || len(created.Password) < 10 {
		t.Fatalf("expected a generated password, got %+v", created)
	}
	if _, ok := hub.accounts.Verify("ana", created.Password); !ok {
		t.Fatal("the password handed back does not actually work")
	}

	// The same name twice is a mistake worth naming.
	w = httptest.NewRecorder()
	hub.ServeAdminAccounts(w, adminRequest(http.MethodPost, "/admin/accounts", `{"name":"ana"}`, "admin", "secret"))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate: expected 409, got %d", w.Code)
	}

	// Listing must never carry a hash to the browser.
	w = httptest.NewRecorder()
	hub.ServeAdminAccounts(w, adminRequest(http.MethodGet, "/admin/accounts", "", "admin", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "$2a$") || strings.Contains(body, "hash") {
		t.Fatalf("a password hash reached the panel:\n%s", body)
	}
	if !strings.Contains(body, `"ana"`) || !strings.Contains(body, `"builtin":true`) {
		t.Fatalf("expected ana and the builtin admin in the list:\n%s", body)
	}

	// Reset the password, then delete the account.
	w = httptest.NewRecorder()
	hub.ServeAdminAccounts(w, adminRequest(http.MethodPut, "/admin/accounts", `{"name":"ana","password":"another good one"}`, "admin", "secret"))
	if w.Code != http.StatusOK {
		t.Fatalf("reset: expected 200, got %d (%s)", w.Code, w.Body)
	}
	if _, ok := hub.accounts.Verify("ana", "another good one"); !ok {
		t.Fatal("the reset password does not work")
	}

	w = httptest.NewRecorder()
	hub.ServeAdminAccounts(w, adminRequest(http.MethodDelete, "/admin/accounts", `{"name":"ana"}`, "admin", "secret"))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d (%s)", w.Code, w.Body)
	}
	if _, ok := hub.accounts.Verify("ana", "another good one"); ok {
		t.Fatal("a deleted account can still log in")
	}
}

// The admin is configured in the environment; the panel must not be able to
// delete the login it is running on, and must say why in a way it can show.
func TestAdminAccountsRefusesToTouchTheBuiltinAdmin(t *testing.T) {
	hub := testHub(config.Config{})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		hub.ServeAdminAccounts(w, adminRequest(method, "/admin/accounts", `{"name":"admin"}`, "admin", "secret"))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s on the builtin admin: expected 403, got %d", method, w.Code)
		}
	}
}

// A password the store would refuse must come back as something the panel can
// put in front of the admin, not a bare 500.
func TestAdminAccountsReportsBadInput(t *testing.T) {
	hub := testHub(config.Config{})
	cases := map[string]string{
		`{"name":"ana","password":"short"}`: "short-password",
		`{"name":"an a"}`:                   "bad-name",
	}
	for body, want := range cases {
		w := httptest.NewRecorder()
		hub.ServeAdminAccounts(w, adminRequest(http.MethodPost, "/admin/accounts", body, "admin", "secret"))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", body, w.Code)
		}
		if !strings.Contains(w.Body.String(), `"code":"`+want+`"`) {
			t.Fatalf("%s: expected code %q, got %q", body, want, w.Body.String())
		}
	}
}

func TestAdminAccountsRequiresAuth(t *testing.T) {
	hub := testHub(config.Config{})
	w := httptest.NewRecorder()
	hub.ServeAdminAccounts(w, httptest.NewRequest(http.MethodGet, "/admin/accounts", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", w.Code)
	}
}
