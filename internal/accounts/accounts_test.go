package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// A real hash takes a couple of hundred milliseconds on purpose. These tests
// care about the rules around it, not about the cost.
func TestMain(m *testing.M) {
	HashCost = bcrypt.MinCost
	os.Exit(m.Run())
}

func tempStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return store, path
}

const goodPassword = "correct horse"

func TestAddVerifyAndReload(t *testing.T) {
	store, path := tempStore(t)
	if err := store.Add("ana", goodPassword); err != nil {
		t.Fatalf("add: %v", err)
	}

	acc, ok := store.Verify("ana", goodPassword)
	if !ok || acc.Role != RoleBroadcaster {
		t.Fatalf("verify: ok=%v role=%q", ok, acc.Role)
	}
	if _, ok := store.Verify("ana", "wrong password"); ok {
		t.Fatal("a wrong password was accepted")
	}
	if _, ok := store.Verify("nobody", goodPassword); ok {
		t.Fatal("an unknown account was accepted")
	}

	// The point of the file: a restart keeps the account.
	reloaded, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok := reloaded.Verify("ana", goodPassword); !ok {
		t.Fatal("account did not survive a reload")
	}
	// And the password itself is nowhere in it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), goodPassword) {
		t.Fatal("the password was written to the file in the clear")
	}
	if entries, _ := filepath.Glob(path + ".tmp"); len(entries) > 0 {
		t.Fatal("a temporary file was left behind")
	}
}

// Names are how a friend types their own login, so they must be forgiving.
func TestNamesAreCaseInsensitiveAndTrimmed(t *testing.T) {
	store, _ := tempStore(t)
	if err := store.Add("  Ana  ", goodPassword); err != nil {
		t.Fatalf("add: %v", err)
	}
	for _, typed := range []string{"ana", "ANA", " Ana "} {
		if _, ok := store.Verify(typed, goodPassword); !ok {
			t.Fatalf("%q did not match the stored account", typed)
		}
	}
	if err := store.Add("ANA", goodPassword); err != ErrExists {
		t.Fatalf("expected ErrExists for a name that differs only in case, got %v", err)
	}
}

func TestPasswordRules(t *testing.T) {
	store, _ := tempStore(t)
	if err := store.Add("ana", "short"); err != ErrShortPassword {
		t.Fatalf("expected ErrShortPassword, got %v", err)
	}
	// bcrypt ignores everything past 72 bytes; a silently truncated password is
	// worse than a refused one.
	if err := store.Add("ana", strings.Repeat("a", 73)); err != ErrLongPassword {
		t.Fatalf("expected ErrLongPassword, got %v", err)
	}
	if err := store.Add("Ana Maria", goodPassword); err != ErrBadName {
		t.Fatalf("expected ErrBadName for a name with a space, got %v", err)
	}
}

// A changed password must take effect at once — the verification cache exists
// for speed, not to keep an old password alive.
func TestPasswordChangeInvalidatesTheCache(t *testing.T) {
	store, _ := tempStore(t)
	if err := store.Add("ana", goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Verify("ana", goodPassword); !ok {
		t.Fatal("setup: first verify failed")
	}

	if err := store.SetPassword("ana", "a whole new password"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, ok := store.Verify("ana", goodPassword); ok {
		t.Fatal("the old password still works after a change")
	}
	if _, ok := store.Verify("ana", "a whole new password"); !ok {
		t.Fatal("the new password does not work")
	}

	if err := store.Remove("ana"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := store.Verify("ana", "a whole new password"); ok {
		t.Fatal("a deleted account can still log in")
	}
	if err := store.Remove("ana"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// The admin comes from the environment: it must never be written to the file,
// and the panel must not be able to delete the login it is using.
func TestBuiltinAdminIsNeitherStoredNorEditable(t *testing.T) {
	store, path := tempStore(t)
	hash, err := Hash(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	store.SetAdmin("boss", hash)

	acc, ok := store.Verify("boss", goodPassword)
	if !ok || acc.Role != RoleAdmin || !acc.Builtin {
		t.Fatalf("admin verify: ok=%v role=%q builtin=%v", ok, acc.Role, acc.Builtin)
	}
	if err := store.Remove("boss"); err != ErrBuiltin {
		t.Fatalf("expected ErrBuiltin on remove, got %v", err)
	}
	if err := store.SetPassword("boss", "another password"); err != ErrBuiltin {
		t.Fatalf("expected ErrBuiltin on password change, got %v", err)
	}

	// Writing the file (by adding someone else) must not drag the admin into it.
	if err := store.Add("ana", goodPassword); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "boss") || strings.Contains(string(data), RoleAdmin) {
		t.Fatalf("the environment admin was written to the accounts file:\n%s", data)
	}
}

// A file that cannot be trusted must stop the server, not be half-loaded:
// starting as if nobody had an account locks everyone out silently, and a file
// that can mint an admin defeats keeping the admin in the environment.
func TestOpenRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"not json":      `nonsense`,
		"role admin":    `[{"name":"eve","role":"admin","hash":"x"}]`,
		"unknown role":  `[{"name":"eve","role":"wizard","hash":"x"}]`,
		"no hash":       `[{"name":"eve","role":"broadcaster","hash":""}]`,
		"invalid name":  `[{"name":"e ve","role":"broadcaster","hash":"x"}]`,
		"name too long": `[{"name":"` + strings.Repeat("e", 33) + `","role":"broadcaster","hash":"x"}]`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "accounts.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("expected an error, got none")
			}
		})
	}
}

// A missing file is the normal first run, not a failure.
func TestOpenMissingFileStartsEmpty(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := len(store.List()); got != 0 {
		t.Fatalf("expected an empty store, got %d accounts", got)
	}
}

func TestGeneratePasswordIsStrongEnoughToStore(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		p := GeneratePassword()
		if seen[p] {
			t.Fatal("GeneratePassword repeated itself")
		}
		seen[p] = true
		if _, err := Hash(p); err != nil {
			t.Fatalf("a generated password was refused by our own rules: %v", err)
		}
	}
}
