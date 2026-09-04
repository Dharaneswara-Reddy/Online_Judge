package main

import (
	"fmt"
	neturl "net/url"
	"strings"
)

// Where the seeder is pointed, and whether it is allowed to write there.
//
// This exists because of a real incident rather than a hypothetical one.
// A `cd` inside a backgrounded subshell never applied to the parent
// shell, so a seed command intended for a scratch database ran from
// server/ instead, godotenv read server/.env, and the seeder wrote to
// production Atlas. Nothing warned, because nothing was looking.
//
// Shell care is not a control: the failure mode is invisible at the call
// site and the blast radius is a production database. So the seeder now
// works out its own target, says it out loud before writing anything,
// and refuses the dangerous answer unless explicitly authorised.
//
// The classification deliberately mirrors assertLocalMongo in the
// integration suite, which already protects the test database the same
// way. Two guards of the same shape are easier to reason about than two
// different ones.

// Environment is the seeder's judgement about what it is connected to.
type Environment string

const (
	// EnvLocal is loopback or a container hostname on a private network:
	// a database it is safe to rewrite at will.
	EnvLocal Environment = "LOCAL"
	// EnvProduction is anything else that resolves. It is the pessimistic
	// reading on purpose — a host this tool does not recognise is treated
	// as somebody's real data.
	EnvProduction Environment = "PRODUCTION"
	// EnvUnknown is a connection string that could not be parsed at all.
	EnvUnknown Environment = "UNKNOWN"
)

// localHosts are the only hosts treated as safe without authorisation:
// loopback, and the service names a MongoDB container gets under Docker
// Compose or a CI service container. It matches the list the integration
// suite uses.
var localHosts = map[string]bool{
	"localhost":  true,
	"127.0.0.1":  true,
	"::1":        true,
	"0.0.0.0":    true,
	"mongo":      true,
	"mongodb":    true,
	"mongo-test": true,
}

// Target is a classified connection, carrying nothing secret.
type Target struct {
	Env    Environment
	Scheme string
	Host   string // host only; userinfo is dropped on construction
	DBName string
}

// classifyTarget works out what a connection string points at.
//
// It never retains the credential: only the scheme, the host and the
// database name survive, so a Target can be logged without redaction.
// An unparseable string is EnvUnknown rather than an error alone,
// because the caller must still be able to print what it could not
// understand.
func classifyTarget(uri, dbName string) (Target, error) {
	t := Target{Env: EnvUnknown, DBName: dbName}

	if strings.TrimSpace(uri) == "" {
		return t, fmt.Errorf("no MongoDB URI is configured")
	}

	parsed, err := neturl.Parse(uri)
	if err != nil {
		return t, fmt.Errorf("MongoDB URI could not be parsed")
	}

	t.Scheme = parsed.Scheme
	if t.Scheme != "mongodb" && t.Scheme != "mongodb+srv" {
		return t, fmt.Errorf("%q is not a MongoDB connection string", t.Scheme)
	}

	t.Host = parsed.Hostname()
	if t.Host == "" {
		return t, fmt.Errorf("MongoDB URI names no host")
	}

	switch {
	case t.Scheme == "mongodb+srv":
		// A seed list served over DNS SRV is an Atlas-shaped deployment.
		// Nobody runs one of those on a laptop.
		t.Env = EnvProduction
	case localHosts[t.Host]:
		t.Env = EnvLocal
	default:
		t.Env = EnvProduction
	}

	return t, nil
}

// Describe renders the target for the line printed before any write.
//
// Host and database only. The connection string it came from contains a
// password, and this string goes to a log.
func (t Target) Describe() string {
	host := t.Host
	if host == "" {
		host = "<unparsed>"
	}
	db := t.DBName
	if db == "" {
		db = "<unset>"
	}
	return fmt.Sprintf("%s  host=%s  database=%s", t.Env, host, db)
}

// guardTarget decides whether the seeder may write.
//
// Local is allowed with no ceremony, because that is the command people
// run twenty times a day and a guard that makes it tedious is a guard
// that gets commented out. Everything else — production, and anything
// this tool could not classify — refuses until somebody has typed the
// flag that says they meant it.
func guardTarget(t Target, allowProduction bool) error {
	if t.Env == EnvLocal {
		return nil
	}
	if allowProduction {
		return nil
	}

	what := "a production database"
	if t.Env == EnvUnknown {
		what = "a database it could not identify"
	}

	return fmt.Errorf(
		"refusing to seed %s (%s).\n"+
			"        If this is deliberate, re-run with --allow-production.\n"+
			"        If it is not, you are probably running from a directory whose\n"+
			"        .env is not the one you intended — check your working directory",
		what, t.Describe())
}
