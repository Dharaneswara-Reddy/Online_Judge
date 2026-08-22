package config

import "testing"

// setRequired fills in the variables Load refuses to start without, so a
// test can concentrate on the one setting it actually cares about.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("MONGO_URI", "mongodb://localhost:27017")
	t.Setenv("JWT_SECRET", "a-test-signing-key-long-enough-to-pass-validation")
}

// The sandbox ceiling is the number of judge containers that may exist on
// the host at once. It defaults low on purpose: the production host is a
// 916MB t3.micro, a sandbox may claim up to 512MB, and the deployment
// budget in deploy/docker-compose.prod.yml has room for exactly one.
func TestLoad_MaxSandboxesDefaultsToOne(t *testing.T) {
	setRequired(t)

	if got := Load().MaxSandboxes; got != 1 {
		t.Errorf("MaxSandboxes default = %d, want 1", got)
	}
}

func TestLoad_MaxSandboxesReadsEnv(t *testing.T) {
	setRequired(t)
	t.Setenv("MAX_SANDBOXES", "3")

	if got := Load().MaxSandboxes; got != 3 {
		t.Errorf("MaxSandboxes = %d, want 3", got)
	}
}

// A ceiling of zero would deadlock every judge, and a negative one is
// meaningless. Both fall back rather than being taken literally.
func TestLoad_MaxSandboxesRejectsNonPositive(t *testing.T) {
	for _, value := range []string{"0", "-4", "unlimited"} {
		t.Run(value, func(t *testing.T) {
			setRequired(t)
			t.Setenv("MAX_SANDBOXES", value)

			if got := Load().MaxSandboxes; got != 1 {
				t.Errorf("MaxSandboxes for %q = %d, want the default of 1", value, got)
			}
		})
	}
}

// WORKER_COUNT sizes queue prefetch. It is a different question from how
// many containers may exist, and conflating the two is what let a single
// configured number become three times as many containers.
func TestLoad_WorkerCountIsIndependentOfMaxSandboxes(t *testing.T) {
	setRequired(t)
	t.Setenv("WORKER_COUNT", "8")
	t.Setenv("MAX_SANDBOXES", "2")

	cfg := Load()
	if cfg.WorkerCount != 8 {
		t.Errorf("WorkerCount = %d, want 8", cfg.WorkerCount)
	}
	if cfg.MaxSandboxes != 2 {
		t.Errorf("MaxSandboxes = %d, want 2", cfg.MaxSandboxes)
	}
}
