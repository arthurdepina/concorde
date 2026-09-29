package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/arthurdepina/concorde/internal/accounts"
	"github.com/arthurdepina/concorde/internal/config"
)

const usage = `Concorde — private screen sharing.

  concorde                     run the server
  concorde hash                print an ADMIN_PASSWORD_HASH line for .env
  concorde account list        list the broadcaster accounts
  concorde account add <name>  create a broadcaster account
  concorde account passwd <n>  change an account's password
  concorde account rm <name>   delete an account

Accounts are friends who may open a room and broadcast. The admin — the only
login that gets the admin panel — is configured with ADMIN_PASSWORD_HASH in the
environment, not with these commands.
`

// runCLI handles everything that is not "start the server". It returns false
// when there was no subcommand to run.
func runCLI(args []string, cfg config.Config) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "hash":
		return true, cmdHash()
	case "account", "accounts":
		return true, cmdAccount(args[1:], cfg)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return true, nil
	default:
		return true, fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

// cmdHash turns a password typed at the prompt into the .env line for it. The
// password is never echoed, never passed as an argument and never stored — only
// the hash is, which is the whole point.
func cmdHash() error {
	password, err := askNewPassword("Admin password")
	if err != nil {
		return err
	}
	hash, err := accounts.Hash(password)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Put this line in your .env (and remove any ADMIN_PASSWORD line):")
	fmt.Println()
	fmt.Printf("ADMIN_PASSWORD_HASH=%s\n", hash)
	return nil
}

func cmdAccount(args []string, cfg config.Config) error {
	store, err := accounts.Open(cfg.AccountsFile)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: concorde account list|add|passwd|rm")
	}

	switch args[0] {
	case "list":
		list := store.List()
		if len(list) == 0 {
			fmt.Printf("No broadcaster accounts yet (%s).\n", store.Path())
			return nil
		}
		fmt.Printf("%s\n\n", store.Path())
		for _, acc := range list {
			fmt.Printf("  %-20s %s\n", acc.Name, acc.Role)
		}
		return nil

	case "add":
		name, err := oneName(args)
		if err != nil {
			return err
		}
		password, err := askNewPassword("Password for " + name)
		if err != nil {
			return err
		}
		if err := store.Add(name, password); err != nil {
			return err
		}
		fmt.Printf("\nCreated %q. They can now open a room with this name and password.\n", name)
		return nil

	case "passwd":
		name, err := oneName(args)
		if err != nil {
			return err
		}
		password, err := askNewPassword("New password for " + name)
		if err != nil {
			return err
		}
		if err := store.SetPassword(name, password); err != nil {
			return err
		}
		fmt.Printf("\nPassword for %q changed.\n", name)
		return nil

	case "rm":
		name, err := oneName(args)
		if err != nil {
			return err
		}
		if err := store.Remove(name); err != nil {
			return err
		}
		fmt.Printf("Deleted %q.\n", name)
		return nil
	}
	return fmt.Errorf("unknown account command %q", args[0])
}

func oneName(args []string) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("usage: concorde account %s <name>", args[0])
	}
	return args[1], nil
}

// askNewPassword prompts twice and checks the two match, so a typo does not
// become a password nobody knows. Typing is hidden on a real terminal; when
// stdin is a pipe (a script) the line is read as-is.
func askNewPassword(prompt string) (string, error) {
	first, err := askPassword(prompt + ": ")
	if err != nil {
		return "", err
	}
	if len([]rune(first)) < accounts.MinPasswordLength {
		return "", accounts.ErrShortPassword
	}
	again, err := askPassword("Repeat: ")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", errors.New("the two passwords do not match")
	}
	return first, nil
}

// One reader for the whole process: a fresh bufio.Reader per prompt would
// swallow whatever it read ahead, so the second prompt would hit EOF on piped
// input.
var stdin = bufio.NewReader(os.Stdin)

func askPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		typed, err := term.ReadPassword(fd)
		fmt.Println()
		return string(typed), err
	}
	line, err := stdin.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// resolveAdmin works out the admin credential this run will use, and returns the
// password to announce when it had to invent one.
//
// There is deliberately no path back to a plaintext password in the
// environment: an ADMIN_PASSWORD left over from before is refused outright
// rather than honoured, because silently accepting it would undo the reason for
// hashing in the first place.
func resolveAdmin(cfg config.Config) (hash, announce string, err error) {
	if cfg.AdminPasswordHash != "" {
		if _, err := bcrypt.Cost([]byte(cfg.AdminPasswordHash)); err != nil {
			return "", "", fmt.Errorf("ADMIN_PASSWORD_HASH is not a bcrypt hash (%w) — generate one with: concorde hash", err)
		}
		if cfg.AdminPasswordPlain != "" {
			return "", "", errors.New("both ADMIN_PASSWORD and ADMIN_PASSWORD_HASH are set — delete the ADMIN_PASSWORD line from your .env")
		}
		return cfg.AdminPasswordHash, "", nil
	}
	if cfg.AdminPasswordPlain != "" {
		return "", "", errors.New("ADMIN_PASSWORD is no longer used: passwords are stored as hashes now.\n" +
			"  Run `concorde hash`, put the ADMIN_PASSWORD_HASH line it prints in your .env, and delete ADMIN_PASSWORD")
	}

	password := accounts.GeneratePassword()
	hashed, err := accounts.Hash(password)
	if err != nil {
		return "", "", err
	}
	return hashed, password, nil
}
