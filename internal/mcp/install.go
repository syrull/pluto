package mcp

import (
	"fmt"
	"strings"
)

// configExample documents the mcp.json shape the agent must produce or extend.
const configExample = `{
  "mcpServers": {
    "<name>": {
      "command": "npx",
      "args": ["-y", "@scope/some-mcp-server"],
      "env": { "SOME_API_KEY": "..." }
    },
    "<remote-name>": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": { "Authorization": "Bearer ..." }
    }
  }
}`

// ConfigExample returns an annotated mcp.json sample for docs and prompts.
func ConfigExample() string { return configExample }

// InstallDirective builds the message /install-mcp hands to the agent. It names
// the repository to investigate, the exact config file to edit, and the rules
// for a safe install: explore first, verify prerequisites, prefer walking the
// user through anything ambiguous, and never invent secrets. The agent uses its
// ordinary tools (bash/read/write/find) to carry it out.
func InstallDirective(repo string) string {
	repo = strings.TrimSpace(repo)
	path := DefaultConfigPath()
	var b strings.Builder
	fmt.Fprintf(&b, "Install the MCP server from the GitHub repository %q into pluto.\n\n", repo)
	b.WriteString("Work through this carefully and show your reasoning:\n\n")
	b.WriteString("1. Explore the repository first. Fetch or clone it (prefer `gh repo view`, " +
		"`git clone` into a temp dir, or read its README via the web) and identify: the MCP " +
		"server's transport (local stdio subprocess vs. remote HTTP/SSE URL), the exact command " +
		"and arguments (or URL) to launch it, any required environment variables, and any " +
		"authentication (API keys, tokens).\n")
	b.WriteString("2. Determine and verify prerequisites. Figure out the runtime it needs " +
		"(Node/npx, Python/uvx/pipx, Docker, a Go/Rust binary, etc.) and check with bash whether " +
		"each is already installed. Report exactly what is present and what is missing.\n")
	b.WriteString("3. If prerequisites are missing, or the setup needs a decision only the user " +
		"can make (installing a runtime, providing an API key or credential), STOP and walk the " +
		"user through those steps instead of guessing. Never fabricate secrets or tokens — leave " +
		"a clear placeholder and tell the user what to fill in.\n")
	fmt.Fprintf(&b, "4. When you have everything, add the server to pluto's MCP config at %q. "+
		"Create the file and its parent directory if they do not exist. If the file already "+
		"exists, MERGE a new entry under \"mcpServers\" and preserve every existing server — read "+
		"it first, do not overwrite it.\n", path)
	b.WriteString("   Also acceptable: ~/.config/pluto/mcp.json (XDG). Use the format:\n\n")
	b.WriteString(configExample)
	b.WriteString("\n\n")
	b.WriteString("   Local servers set command/args/env; remote servers set type:\"http\" (or " +
		"\"sse\") with url/headers. Omit \"type\" for stdio.\n")
	b.WriteString("5. Finish with a short summary: which server you added, the transport, the " +
		"prerequisites, anything the user still must do, and remind them that pluto loads MCP " +
		"servers at startup — they must restart pluto for the new server's tools to appear.\n")
	return b.String()
}
