// Package agentguide carries the canonical agent workflow skill. Client
// adapters distribute this copy; TestInstalledGuideMatchesPlugin in
// internal/clientconfig keeps the plugin payload in sync with it.
package guide

import _ "embed"

//go:embed using-agent-camp.md
var Markdown string

// Name is the skill name clients see.
const Name = "using-agent-camp"
