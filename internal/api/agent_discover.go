package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"

	"github.com/tim4net/agent-os/internal/db"
)

// KnownAgent defines a known agent hostname and its default harness type.
type KnownAgent struct {
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name"`
	Harness     string `json:"harness"`
	BaseURL     string `json:"base_url"`
}

// knownAgents is the mapping of hostnames to harness types.
var knownAgents = []KnownAgent{
	{Hostname: "roux", DisplayName: "Roux", Harness: "hermes", BaseURL: "http://roux:8080"},
	{Hostname: "crawbot", DisplayName: "Crawbot", Harness: "openclaw", BaseURL: "http://crawbot:8080"},
	{Hostname: "xps", DisplayName: "XPS", Harness: "litellm", BaseURL: "http://xps:4000"},
}

// DiscoveredAgent represents an agent found on the tailnet but not yet registered.
type DiscoveredAgent struct {
	KnownAgent
	Online bool `json:"online"`
}

// DiscoverAgents handles GET /api/agents/discover
func (a *API) DiscoverAgents(w http.ResponseWriter, r *http.Request) {
	// Get currently registered agents
	registered, err := a.queries.ListAgents(r.Context())
	if err != nil {
		slog.Error("failed to list agents for discovery", "error", err)
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	// Build set of registered agent names
	registeredNames := make(map[string]bool)
	for _, agent := range registered {
		registeredNames[agent.Name] = true
	}

	// Get tailscale status to check which agents are online
	onlineHosts := getTailscaleOnlineHosts()

	// Find unregistered agents
	var discovered []DiscoveredAgent
	for _, ka := range knownAgents {
		if !registeredNames[ka.Hostname] {
			discovered = append(discovered, DiscoveredAgent{
				KnownAgent: ka,
				Online:     onlineHosts[ka.Hostname],
			})
		}
	}

	if discovered == nil {
		discovered = []DiscoveredAgent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"discovered": discovered,
		"total":      len(discovered),
	})
}

// AutoRegisterAgents handles POST /api/agents/auto-register
func (a *API) AutoRegisterAgents(w http.ResponseWriter, r *http.Request) {
	// Get currently registered agents
	registered, err := a.queries.ListAgents(r.Context())
	if err != nil {
		slog.Error("failed to list agents for auto-register", "error", err)
		http.Error(w, "failed to list agents", http.StatusInternalServerError)
		return
	}

	registeredNames := make(map[string]bool)
	for _, agent := range registered {
		registeredNames[agent.Name] = true
	}

	var registered_agents []db.Agent
	for _, ka := range knownAgents {
		if registeredNames[ka.Hostname] {
			continue
		}

		agent, err := a.queries.CreateAgent(r.Context(), db.CreateAgentParams{
			Name:        ka.Hostname,
			DisplayName: ka.DisplayName,
			Harness:     ka.Harness,
			BaseUrl:     ka.BaseURL,
			Metadata:    []byte(`{"auto_registered": true, "discovered": true}`),
		})
		if err != nil {
			slog.Error("failed to auto-register agent", "hostname", ka.Hostname, "error", err)
			continue
		}

		slog.Info("auto-registered agent", "hostname", ka.Hostname, "harness", ka.Harness, "id", agent.ID.String())
		registered_agents = append(registered_agents, agent)
	}

	if registered_agents == nil {
		registered_agents = []db.Agent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"registered": registered_agents,
		"count":      len(registered_agents),
	})
}

// getTailscaleOnlineHosts runs `tailscale status` and returns a set of online hostnames.
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
			Online  bool   `json:"Online"`
		} `json:"Peer"`
		Self struct {
			HostName string `json:"HostName"`
		} `json:"Self"`
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
func parseTailscaleStatusText(output string) map[string]bool {
	hosts := make(map[string]bool)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// Typical format: "hostname 100.x.x.x:xxxx   idle   linux   -"
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			name := strings.ToLower(fields[0])
			for _, ka := range knownAgents {
				if name == ka.Hostname {
					hosts[name] = true
				}
			}
		}
	}
	return hosts
}
