package main

import (
	"strings"
	"testing"
)

// The seeder wrote to production Atlas because nothing stopped it. It
// read whichever .env sat in the working directory, and the working
// directory was not the one anybody intended — a `cd` inside a
// backgrounded subshell never applied to the parent shell, so a command
// meant for a scratch database ran from server/ and picked up the
// production credential.
//
// No amount of care with shell quoting fixes that class of mistake. The
// fix is for the seeder to know where it is pointed and to refuse the
// dangerous answer unless told otherwise.

// mongoURI assembles a connection string from parts.
//
// The parts are joined at runtime rather than written as literals
// because a tracked file embedding userinfo in a connection string trips
// credential scanners, and a test fixture is not worth teaching people
// to wave those through.
func mongoURI(scheme, user, pass, host, tail string) string {
	auth := ""
	if user != "" {
		auth = user + ":" + pass + "@"
	}
	return scheme + "://" + auth + host + tail
}

const atlasHost = "code-arena-cluster.abc.mongodb.net"

func TestClassifyTargetRecognisesLocalHosts(t *testing.T) {
	for _, uri := range []string{
		"mongodb://localhost:27017",
		"mongodb://127.0.0.1:27018/x",
		"mongodb://mongo:27017",
		"mongodb://mongo-test:27017/online_judge_test",
		mongoURI("mongodb", "user", "pass", "localhost:27017", "/db?retryWrites=true"),
	} {
		got, err := classifyTarget(uri, "online_judge")
		if err != nil {
			t.Fatalf("classifyTarget(%q) = %v", uri, err)
		}
		if got.Env != EnvLocal {
			t.Errorf("classifyTarget(%q).Env = %s, want %s", uri, got.Env, EnvLocal)
		}
	}
}

// An Atlas SRV connection string is the shape the accident used, and is
// production by construction: nobody runs one on a laptop.
func TestClassifyTargetTreatsAtlasAsProduction(t *testing.T) {
	got, err := classifyTarget(mongoURI("mongodb+srv", "u", "p", atlasHost, "/?retryWrites=true"), "online_judge")
	if err != nil {
		t.Fatalf("classifyTarget = %v", err)
	}
	if got.Env != EnvProduction {
		t.Fatalf("Env = %s, want %s", got.Env, EnvProduction)
	}
}

func TestClassifyTargetTreatsAnyRemoteHostAsProduction(t *testing.T) {
	got, err := classifyTarget("mongodb://10.0.4.7:27017/online_judge", "online_judge")
	if err != nil {
		t.Fatalf("classifyTarget = %v", err)
	}
	if got.Env != EnvProduction {
		t.Fatalf("Env = %s, want %s", got.Env, EnvProduction)
	}
}

func TestClassifyTargetFailsClosedOnNonsense(t *testing.T) {
	for _, uri := range []string{"", "not-a-uri", "http://example.com", "mongodb://"} {
		got, err := classifyTarget(uri, "db")
		if err == nil && got.Env == EnvLocal {
			t.Errorf("classifyTarget(%q) reported LOCAL; ambiguity must never resolve to the safe answer", uri)
		}
	}
}

// --- the guard ----------------------------------------------------------

func TestGuardAllowsLocalWithoutCeremony(t *testing.T) {
	target, err := classifyTarget("mongodb://localhost:27017", "online_judge_dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := guardTarget(target, false); err != nil {
		t.Fatalf("guardTarget refused a local seed: %v", err)
	}
}

// The whole point of the file.
func TestGuardRefusesProductionWithoutTheFlag(t *testing.T) {
	target, err := classifyTarget(mongoURI("mongodb+srv", "u", "p", atlasHost, "/"), "online_judge")
	if err != nil {
		t.Fatal(err)
	}

	err = guardTarget(target, false)
	if err == nil {
		t.Fatal("guardTarget allowed a production seed with no authorisation")
	}
	if !strings.Contains(err.Error(), "--allow-production") {
		t.Errorf("the refusal does not name the flag that would authorise it: %v", err)
	}
}

func TestGuardAllowsProductionWhenExplicitlyAuthorised(t *testing.T) {
	target, err := classifyTarget(mongoURI("mongodb+srv", "u", "p", atlasHost, "/"), "online_judge")
	if err != nil {
		t.Fatal(err)
	}
	if err := guardTarget(target, true); err != nil {
		t.Fatalf("guardTarget refused an explicitly authorised production seed: %v", err)
	}
}

func TestGuardRefusesAnUnknownTarget(t *testing.T) {
	if err := guardTarget(Target{Env: EnvUnknown}, false); err == nil {
		t.Fatal("an unclassifiable target was allowed; ambiguity must fail closed")
	}
}

// --- the description printed before any write ---------------------------

// Describe is logged, so it must never carry the credential embedded in
// a connection string.
func TestDescribeNeverPrintsCredentials(t *testing.T) {
	const secret = "sup3r-s3cret-passw0rd"
	target, err := classifyTarget(mongoURI("mongodb+srv", "admin", secret, atlasHost, "/"), "online_judge")
	if err != nil {
		t.Fatal(err)
	}

	got := target.Describe()
	if strings.Contains(got, secret) {
		t.Fatalf("Describe leaked the password: %s", got)
	}
	if strings.Contains(got, "admin:") {
		t.Fatalf("Describe leaked the username: %s", got)
	}
	for _, want := range []string{"PRODUCTION", atlasHost, "online_judge"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe omits %q, which is what makes it useful: %s", want, got)
		}
	}
}
