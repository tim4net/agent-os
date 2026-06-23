package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"

	"github.com/tim4net/agent-os/internal/config"
	"github.com/tim4net/agent-os/internal/db"
)

// DiscoveredAgent represents an agent present in the fleet manifest but not yet
// registered. The candidate set is config-driven (issue #136) and filtered by
// registered harness types.
type DiscoveredAgent struct {
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name"`
	Harness     string `json:"harness"`
	BaseURL     string `json:"base_url"`
	Online      bool   `json:"online"`
}

// manifest returns the API's fleet manifest, falling back to the config default
// when unset (e.g. in unit tests that build &API{} literals directly).
func (a *API) manifest() []config.AgentSpec {
	if a.agentManifest != nil {
		return a.agentManifest
	}
	return config.DefaultAgentSpecs()
}

// harnessKnown reports whether a harness type is registered. A nil registry is
// treated as "all known" so discovery degrades gracefully rather than
// returning an empty fleet.
func (a *API) harnessKnown(name string) bool {
	if a.registry == nil {
		return true
	}
	for _, n := range a.registry.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// computeDiscovered derives the discoverable agent list purely from its inputs
// (no I/O) so it is unit-testable. Candidates come from the manifest (config),
// excluding already-registered agents and agents whose harness type is not
// registered. This is the core of issue #136: the fleet is assembled from
// registered sources (manifest + registry), not a hardcoded Go list.
func computeDiscovered(manifest []config.AgentSpec, registered map[string]bool, online map[string]bool, isHarnessKnown func(string) bool) []DiscoveredAgent {
	out := []DiscoveredAgent{}
	for _, s := range manifest {
		if registered[s.Hostname] {
			continue
		}
		if isHarnessKnown != nil && !isHarnessKnown(s.Harness) {
			slog.Warn("discovery: skipping agent with unregistered harness", "hostname", s.Hostname, "harness", s.Harness)
			continue
		}
		out = append(out, DiscoveredAgent{
			Hostname:    s.Hostname,
			DisplayName: s.DisplayName,
			Harness:     s.Harness,
			BaseURL:     s.BaseURL,
			Online:      online[s.Hostname],
		})
	}
	return out
}

// unregisteredCandidates returns manifest entries that are not yet registered
// and whose harness type is registered — the set auto-register will create.
func unregisteredCandidates(manifest []config.AgentSpec, registered map[string]bool, isHarnessKnown func(string) bool) []config.AgentSpec {
	out := []config.AgentSpec{}
	for _, s := range manifest {
		if registered[s.Hostname] {
			continue
		}
		if isHarnessKnown != nil && !isHarnessKnown(s.Harness) {
			slog.Warn("auto-register: skipping agent with unregistered harness", "hostname", s.Hostname, "harness", s.Harness)
			continue
		}
		out = append(out, s)
	}
	return out
}

// DiscoverAgents handles GET /api/agents/discover
func (a *API) DiscoverAgents(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	// Get currently registered agents
	registered, err := a.queries.ListAgents(r.Context(), ownerID)
	if err != nil {
		slog.Error("failed to list agents for discovery", "error", err)
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	// Build set of registered agent names
	registeredNames := make(map[string]bool, len(registered))
	for _, agent := range registered {
		registeredNames[agent.Name] = true
	}

	// Candidates are derived from the config-driven manifest, not a hardcoded list.
	discovered := computeDiscovered(a.manifest(), registeredNames, getTailscaleOnlineHosts(), a.harnessKnown)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"discovered": discovered,
		"total":      len(discovered),
	})
}

// AutoRegisterAgents handles POST /api/agents/auto-register
func (a *API) AutoRegisterAgents(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := OwnerIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized: no owner identity", http.StatusUnauthorized)
		return
	}

	// Get currently registered agents
	registered, err := a.queries.ListAgents(r.Context(), ownerID)
	if err != nil {
		slog.Error("failed to list agents for auto-register", "error", err)
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	registeredNames := make(map[string]bool, len(registered))
	for _, agent := range registered {
		registeredNames[agent.Name] = true
	}

	candidates := unregisteredCandidates(a.manifest(), registeredNames, a.harnessKnown)

	var created []db.Agent
	for _, c := range candidates {
		agent, err := a.queries.EnsureAgent(r.Context(), db.EnsureAgentParams{
			OwnerID:     ownerID,
			Name:        c.Hostname,
			DisplayName: c.DisplayName,
			Harness:     c.Harness,
			BaseUrl:     c.BaseURL,
			Metadata:    []byte(`{"auto_registered": true, "discovered": true}`),
		})
		if err != nil {
			// err == pgx.ErrNoRows means agent was created by a concurrent request — fetch it.
			existing, getErr := a.queries.GetAgentByName(r.Context(), db.GetAgentByNameParams{
				Name:    c.Hostname,
				OwnerID: ownerID,
			})
			if getErr != nil {
				slog.Error("failed to auto-register agent", "hostname", c.Hostname, "error", err)
				continue
			}
			agent = existing
		}

		slog.Info("auto-registered agent", "hostname", c.Hostname, "harness", c.Harness, "id", agent.ID.String())
		created = append(created, agent)
	}

	if created == nil {
		created = []db.Agent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"registered": sanitizeAgents(created),
		"count":      len(created),
	})
}

// getTailscaleOnlineHosts runs `tailscale status` and returns a set of online
// hostnames. It returns ALL online peers; the caller filters by manifest
// membership, so it no longer depends on a hardcoded agent list.
func getTailscaleOnlineHosts() map[string]bool {
	hosts := make(map[string]bool)

	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		slog.Warn("failed to run tailscale status", "error", err)
		return hosts
	}

	// Parse the JSON output to find online peers
	var status struct {
		Peer map[string]struct {
			HostName string `json:"HostName"`
			Online   bool   `json:"Online"`
		} `json:"Peer"`
	}

	if err := json.Unmarshal(out, &status); err != nil {
		// Fallback: try parsing as plain text
		slog.Warn("failed to parse tailscale status JSON, using text fallback", "error", err)
		return parseTailscaleStatusText(string(out))
	}

	for _, peer := range status.Peer {
		if peer.Online {
			name := strings.ToLower(strings.Split(peer.HostName, ".")[0])
			hosts[name] = true
		}
	}

	return hosts
}

// parseTailscaleStatusText is a fallback for when JSON output isn't available.
// It returns every hostname seen in the status text (lowercased first label)
// rather than filtering to a hardcoded known set.
func parseTailscaleStatusText(output string) map[string]bool {
	hosts := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		// Typical format: "hostname 100.x.x.x:xxxx   idle   linux   -"
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			name := strings.ToLower(fields[0])
			if name == "" || strings.HasPrefix(name, "#") {
				continue
			}
			hosts[name] = true
		}
	}
	return hosts
}
