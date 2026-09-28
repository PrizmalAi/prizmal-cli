package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The harness-launch workflow hands a live production Switch credential to
// five jobs that each install unpinned third-party software (two `curl |
// bash` installers and three `npm install -g ...@latest`). These tests pin
// the blast radius of that credential: it must reach exactly one step per
// job, the legs must genuinely skip rather than report a green pass when the
// secret is absent, and a superseded push must not leave five live launches
// running.

const harnessLaunchWorkflowPath = ".github/workflows/harness-launch.yml"

// secretGateJob is the job whose sole purpose is to turn the presence of the
// secret into a boolean the launch legs can gate on at JOB level. A job-level
// `if` cannot see `secrets`, and it must not depend on job-level `env` --
// that is precisely the thing we are removing.
const secretGateJob = "check-secret"

const switchKeyEnv = "PRIZMAL_SWITCH_KEY"

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	If   string            `yaml:"if"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

type workflowJob struct {
	If    string            `yaml:"if"`
	Needs yaml.Node         `yaml:"needs"`
	Env   map[string]string `yaml:"env"`
	Steps []workflowStep    `yaml:"steps"`
}

type harnessWorkflow struct {
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress bool   `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

func loadHarnessLaunchWorkflow(t *testing.T) harnessWorkflow {
	t.Helper()
	raw, err := os.ReadFile(harnessLaunchWorkflowPath)
	if err != nil {
		t.Fatalf("reading %s: %v", harnessLaunchWorkflowPath, err)
	}
	var wf harnessWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parsing %s: %v", harnessLaunchWorkflowPath, err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatalf("%s declares no jobs", harnessLaunchWorkflowPath)
	}
	return wf
}

// launchJobs returns every job except the secret gate, keyed by harness name.
func launchJobs(t *testing.T, wf harnessWorkflow) map[string]workflowJob {
	t.Helper()
	if _, ok := wf.Jobs[secretGateJob]; !ok {
		t.Fatalf("%s has no %q job; without it the launch legs cannot gate on the secret at job level", harnessLaunchWorkflowPath, secretGateJob)
	}
	legs := make(map[string]workflowJob, len(wf.Jobs)-1)
	for name, job := range wf.Jobs {
		if name == secretGateJob {
			continue
		}
		legs[name] = job
	}
	return legs
}

func needsList(t *testing.T, node yaml.Node) []string {
	t.Helper()
	if node.IsZero() {
		return nil
	}
	var one string
	if err := node.Decode(&one); err == nil {
		return []string{one}
	}
	var many []string
	if err := node.Decode(&many); err != nil {
		t.Fatalf("decoding `needs`: %v", err)
	}
	return many
}

// TestHarnessLaunchKeyIsScopedToTheLaunchStep is the supply-chain pin: the
// live credential must not be in the environment of `curl | bash`, of
// `npm install -g ...@latest`, or of the setup-go cache restore.
func TestHarnessLaunchKeyIsScopedToTheLaunchStep(t *testing.T) {
	wf := loadHarnessLaunchWorkflow(t)
	for name, job := range launchJobs(t, wf) {
		if _, leaked := job.Env[switchKeyEnv]; leaked {
			t.Errorf("job %q declares %s at job level, which puts the live key in the environment of every step including the unpinned third-party installers", name, switchKeyEnv)
		}
		var carriers []string
		for _, step := range job.Steps {
			if _, ok := step.Env[switchKeyEnv]; ok {
				carriers = append(carriers, step.Name)
			}
		}
		if len(carriers) != 1 {
			t.Errorf("job %q: expected exactly 1 step to receive %s, got %d: %v", name, switchKeyEnv, len(carriers), carriers)
			continue
		}
		if !strings.HasPrefix(carriers[0], "Launch ") {
			t.Errorf("job %q: %s reaches step %q; only the launch step needs it", name, switchKeyEnv, carriers[0])
		}
	}
}

// TestHarnessLaunchLegsSkipRatherThanPassWithoutTheSecret pins that a run
// with no secret reports *skipped*. With the previous step-level guards the
// job reported SUCCESS having launched nothing -- so if these ever become
// required checks, rotating the secret away would turn all five green.
func TestHarnessLaunchLegsSkipRatherThanPassWithoutTheSecret(t *testing.T) {
	wf := loadHarnessLaunchWorkflow(t)
	for name, job := range launchJobs(t, wf) {
		needs := needsList(t, job.Needs)
		found := false
		for _, n := range needs {
			if n == secretGateJob {
				found = true
			}
		}
		if !found {
			t.Errorf("job %q does not declare `needs: %s`, so it cannot gate on the secret at job level", name, secretGateJob)
		}
		if !strings.Contains(job.If, "needs."+secretGateJob+".outputs") {
			t.Errorf("job %q job-level `if` does not consult the %s outputs, so a secretless run would report passed instead of skipped: %q", name, secretGateJob, job.If)
		}
		for _, step := range job.Steps {
			if strings.Contains(step.If, "env."+switchKeyEnv) {
				t.Errorf("job %q step %q still guards on env.%s; that context resolves against job-level env, which no longer carries the key", name, step.Name, switchKeyEnv)
			}
		}
	}
}

// TestHarnessLaunchCancelsSupersededRuns pins that a second push does not
// leave the first push's five live production launches running.
func TestHarnessLaunchCancelsSupersededRuns(t *testing.T) {
	wf := loadHarnessLaunchWorkflow(t)
	if wf.Concurrency.Group == "" {
		t.Fatalf("%s declares no concurrency group; every push starts five more live launches", harnessLaunchWorkflowPath)
	}
	if !strings.Contains(wf.Concurrency.Group, "github.ref") {
		t.Errorf("concurrency group %q is not keyed on the ref", wf.Concurrency.Group)
	}
	if !wf.Concurrency.CancelInProgress {
		t.Errorf("concurrency group %q does not cancel in-progress runs", wf.Concurrency.Group)
	}
}

// TestHarnessLaunchKeepsTheForkGuard keeps the belt-and-braces check that no
// leg runs for a pull request from a fork, on top of Actions never handing
// secrets to fork `pull_request` runs.
func TestHarnessLaunchKeepsTheForkGuard(t *testing.T) {
	wf := loadHarnessLaunchWorkflow(t)
	gate := wf.Jobs[secretGateJob]
	if !strings.Contains(gate.If, "head.repo.full_name") {
		t.Errorf("job %q lost the fork guard: %q", secretGateJob, gate.If)
	}
	for name, job := range launchJobs(t, wf) {
		if !strings.Contains(job.If, "head.repo.full_name") {
			t.Errorf("job %q lost the fork guard: %q", name, job.If)
		}
	}
}

// TestHarnessLaunchLegsNameARealModel pins that every live leg resolves a model
// from the CI key's own tenant.
//
// A launch carries a real model now: the reserved prizmal/default placeholder
// is sunset, and with no placeholder to fall back on a bare `prizmal <harness>`
// in a non-interactive job stops before launching anything. Every leg must
// therefore pass -m, and the value must come from --list rather than a literal
// that a tenant without that model would fail on.
func TestHarnessLaunchLegsNameARealModel(t *testing.T) {
	wf := loadHarnessLaunchWorkflow(t)

	for name, job := range launchJobs(t, wf) {
		launch := launchStep(t, name, job)

		if strings.Contains(launch.Run, "prizmal/default") {
			t.Errorf("job %q still launches with the sunset prizmal/default name", name)
		}
		if !strings.Contains(launch.Run, "prizmal --list") {
			t.Errorf("job %q does not read its model from --list, so it names no model the CI key is known to serve", name)
		}
		if !strings.Contains(launch.Run, `-m "$MODEL"`) {
			t.Errorf("job %q does not pass the model it resolved; a bare launch now stops before launching", name)
		}
	}
}

// launchStep returns the one step per job that carries the Switch key, which is
// the step that launches the harness.
func launchStep(t *testing.T, name string, job workflowJob) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if _, ok := step.Env[switchKeyEnv]; ok {
			return step
		}
	}
	t.Fatalf("job %q has no step carrying %s, so there is nothing to check", name, switchKeyEnv)
	return workflowStep{}
}

// TestHarnessLaunchLegsTakeTheFirstModelWithoutHead pins the reason the legs use
// sed rather than head.
//
// Every leg runs under `set -o pipefail`. `head -n 1` exits as soon as it has
// its line, so the writer takes SIGPIPE and the step dies with 141, which is
// how a leg failed once: the model read worked, and the step was killed before
// it launched anything. `sed -n 1p` reads the whole stream, so no writer is
// signalled. This has to stay, or the failure returns as a flake that looks
// like a harness problem.
func TestHarnessLaunchLegsTakeTheFirstModelWithoutHead(t *testing.T) {
	wf := loadHarnessLaunchWorkflow(t)

	for name, job := range launchJobs(t, wf) {
		launch := launchStep(t, name, job)

		if strings.Contains(launch.Run, "--list | head") {
			t.Errorf("job %q pipes --list into head; under `set -o pipefail` that exits 141 (SIGPIPE) and kills the step", name)
		}
		if !strings.Contains(launch.Run, "set -o pipefail") {
			t.Errorf("job %q dropped `set -o pipefail`, so this check no longer reflects how the step runs", name)
		}
	}
}
