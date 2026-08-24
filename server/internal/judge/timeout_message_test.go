package judge

import (
	"strings"
	"testing"
)

// A compile that runs out of time and a program that runs out of time are
// different failures with different fixes, and they used to produce the
// same sentence: "Command execution timed out". Surfaced under a
// compile_error verdict, that reads as though the code did not compile,
// which sends the author looking for a syntax mistake that is not there.
//
// The five outcomes a user has to be able to tell apart are: compilation
// timeout, execution timeout, compile error, runtime error, and an
// infrastructure failure. The verdicts already separate the last four;
// only the wording separates the first from a genuine compile error, so
// the wording has to carry it.
func TestTimeoutMessage_NamesThePhase(t *testing.T) {
	compile := timeoutMessage(phaseCompile, compileTimeout)
	run := timeoutMessage(phaseRun, 2000000000)

	if !strings.Contains(strings.ToLower(compile), "compil") {
		t.Errorf("compile timeout message does not mention compilation: %q", compile)
	}
	if strings.Contains(strings.ToLower(compile), "execution timed out") {
		t.Errorf("compile timeout still reads as an execution timeout: %q", compile)
	}
	if compile == run {
		t.Errorf("both phases produce the same message: %q", compile)
	}
	// The budget belongs in the message: "timed out" without a number
	// leaves the author guessing what they have to fit inside.
	if !strings.Contains(compile, "15s") {
		t.Errorf("compile timeout message omits the budget: %q", compile)
	}
	if !strings.Contains(strings.ToLower(run), "execution") && !strings.Contains(strings.ToLower(run), "program") {
		t.Errorf("run timeout message does not describe execution: %q", run)
	}
}

// A compile timeout is still a compile_error verdict. Introducing a new
// verdict would mean a new status the client and the stored submissions
// have never seen, and the distinction the user needs is carried by the
// message.
func TestTimeoutMessage_CompileTimeoutIsDistinguishableFromCompilerOutput(t *testing.T) {
	msg := timeoutMessage(phaseCompile, compileTimeout)
	if msg == "" {
		t.Fatal("compile timeout message must not be empty — an empty CompileError renders as a blank error")
	}
	// It must not look like compiler diagnostics, which is what the field
	// normally carries.
	if strings.Contains(msg, "error:") {
		t.Errorf("compile timeout message imitates compiler output: %q", msg)
	}
}
