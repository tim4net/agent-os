package api

import (
	"testing"

	"github.com/tim4net/agent-os/internal/config"
	"github.com/tim4net/agent-os/internal/harness"
)

// allKnown is an isHarnessKnown predicate that admits everything (simulates a
// nil registry or a fully-registered fleet).
func allKnown(string) bool { return true }

func TestComputeDiscovered_ConfigDriven(t *testing.T) {
	// The candidate set is whatever the manifest says — not a hardcoded list.
	manifest := []config.AgentSpec{
		{Hostname: "alpha", DisplayName: "Alpha", Harness: "hermes", BaseURL: "http://alpha"},
		{Hostname: "beta", DisplayName: "Beta", Harness: "agy", BaseURL: "local://beta"},
	}
	got := computeDiscovered(manifest, map[string]bool{}, map[string]bool{}, allKnown)
	if len(got) != 2 {
		t.Fatalf("expected 2 discovered, got %d (%+v)", len(got), got)
	}
	if got[0].Hostname != "alpha" || got[0].Harness != "hermes" {
		t.Errorf("unexpected first: %+v", got[0])
	}
	// JSON shape must still carry the stable fields.
	if got[1].BaseURL != "local://beta" || got[1].DisplayName != "Beta" {
		t.Errorf("unexpected second: %+v", got[1])
	}
}

func TestComputeDiscovered_ExcludesRegistered(t *testing.T) {
	manifest := []config.AgentSpec{
		{Hostname: "alpha", Harness: "hermes", BaseURL: "u1"},
		{Hostname: "beta", Harness: "agy", BaseURL: "u2"},
	}
	registered := map[string]bool{"alpha": true} // alpha already registered
	got := computeDiscovered(manifest, registered, map[string]bool{}, allKnown)
	if len(got) != 1 || got[0].Hostname != "beta" {
		t.Fatalf("registered agent must be excluded; got %+v", got)
	}
}

func TestComputeDiscovered_OnlineFlag(t *testing.T) {
	manifest := []config.AgentSpec{
		{Hostname: "alpha", Harness: "hermes", BaseURL: "u1"},
		{Hostname: "beta", Harness: "hermes", BaseURL: "u2"},
	}
	online := map[string]bool{"alpha": true}
	got := computeDiscovered(manifest, map[string]bool{}, online, allKnown)
	byName := map[string]bool{}
	for _, d := range got {
		byName[d.Hostname] = d.Online
	}
	if !byName["alpha"] {
		t.Error("alpha should be online")
	}
	if byName["beta"] {
		t.Error("beta should be offline")
	}
}

func TestComputeDiscovered_RegistryFilterExcludesUnknownHarness(t *testing.T) {
	// NEGATIVE proof: an agent whose harness type is NOT registered in the
	// harness registry is filtered out of discovery. This is the "builds on the
	// harness registry" acceptance criterion (#136).
	reg := harness.NewRegistry()
	reg.Register("hermes", harness.NewHermesHarness)
	isKnown := func(name string) bool {
		for _, n := range reg.Names() {
			if n == name {
				return true
			}
		}
		return false
	}
	manifest := []config.AgentSpec{
		{Hostname: "good", Harness: "hermes", BaseURL: "u1"},
		{Hostname: "bad", Harness: "does-not-exist", BaseURL: "u2"},
	}
	got := computeDiscovered(manifest, map[string]bool{}, map[string]bool{}, isKnown)
	if len(got) != 1 || got[0].Hostname != "good" {
		t.Fatalf("unregistered-harness candidate must be excluded; got %+v", got)
	}
}

func TestComputeDiscovered_EmptyManifestYieldsEmpty(t *testing.T) {
	// AGENTS_JSON='[]' must disable discovery entirely.
	got := computeDiscovered([]config.AgentSpec{}, map[string]bool{}, map[string]bool{}, allKnown)
	if len(got) != 0 {
		t.Fatalf("empty manifest must yield no candidates; got %+v", got)
	}
}

func TestUnregisteredCandidates_RegistryFilter(t *testing.T) {
	reg := harness.NewRegistry()
	reg.Register("hermes", harness.NewHermesHarness)
	isKnown := func(name string) bool {
		for _, n := range reg.Names() {
			if n == name {
				return true
			}
		}
		return false
	}
	manifest := []config.AgentSpec{
		{Hostname: "reg-already", Harness: "hermes", BaseURL: "u0"},
		{Hostname: "good", Harness: "hermes", BaseURL: "u1"},
		{Hostname: "bad-harness", Harness: "nope", BaseURL: "u2"},
	}
	registered := map[string]bool{"reg-already": true}
	got := unregisteredCandidates(manifest, registered, isKnown)
	if len(got) != 1 || got[0].Hostname != "good" {
		t.Fatalf("expected only 'good' candidate; got %+v", got)
	}
}

func TestHarnessKnown_RealRegistry(t *testing.T) {
	reg := harness.NewRegistry()
	reg.Register("hermes", harness.NewHermesHarness)
	a := &API{registry: reg}
	if !a.harnessKnown("hermes") {
		t.Error("hermes should be known")
	}
	if a.harnessKnown("phantom") {
		t.Error("phantom should NOT be known")
	}
}

func TestHarnessKnown_NilRegistryAdmitsAll(t *testing.T) {
	// A nil registry (e.g. gen-openapi, some tests) must not blank the fleet.
	a := &API{registry: nil}
	if !a.harnessKnown("anything") {
		t.Error("nil registry should admit all harnesses (degrade gracefully)")
	}
}

func TestManifest_FallsBackToDefaultWhenUnset(t *testing.T) {
	// &API{} literals (used widely in tests) get the config default manifest.
	a := &API{}
	m := a.manifest()
	if len(m) != 4 {
		t.Fatalf("default manifest should have 4 agents, got %d", len(m))
	}
}

func TestManifest_UsesExplicitField(t *testing.T) {
	custom := []config.AgentSpec{{Hostname: "custom", Harness: "hermes", BaseURL: "u"}}
	a := &API{agentManifest: custom}
	m := a.manifest()
	if len(m) != 1 || m[0].Hostname != "custom" {
		t.Fatalf("explicit manifest must win; got %+v", m)
	}
}
