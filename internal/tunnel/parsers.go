package tunnel

import (
	"encoding/json"
	"regexp"
	"strings"
)

// parseCloudflaredURL finds the quick-tunnel URL cloudflared prints.
func parseCloudflaredURL(output string) string {
	m := cloudflaredURL.FindString(output)
	if m == "" {
		return ""
	}
	return m
}

// parseNgrokTunnels extracts the public URL from the ngrok local API's
// /api/tunnels JSON. HTTPS is preferred.
func parseNgrokTunnels(data []byte) string {
	var doc struct {
		Tunnels []struct {
			PublicURL string `json:"public_url"`
		} `json:"tunnels"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	fallback := ""
	for _, t := range doc.Tunnels {
		if strings.HasPrefix(t.PublicURL, "https://") {
			return t.PublicURL
		}
		if fallback == "" && t.PublicURL != "" {
			fallback = t.PublicURL
		}
	}
	return fallback
}

var (
	tsHTTPSURL = regexp.MustCompile(`https://[a-z0-9-]+(\.[a-z0-9-]+)*\.ts\.net(:\d+)?`)
	tsTCPURL   = regexp.MustCompile(`tcp://([a-z0-9-]+(\.[a-z0-9-]+)*\.ts\.net):\d+`)
)

// parseTailscaleStatus extracts the public funnel URL from
// `tailscale funnel status` output. The documented sample shows lines like
// `|-- tcp://host.ts.net:443`; an https:// URL is used directly when present.
func parseTailscaleStatus(output string) string {
	if u := tsHTTPSURL.FindString(output); u != "" {
		return u
	}
	if m := tsTCPURL.FindStringSubmatch(output); m != nil {
		return "https://" + m[1]
	}
	return ""
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	return lines
}

func joinLines(lines []string) string { return strings.Join(lines, " | ") }
