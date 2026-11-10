// Package agentguide carries the canonical agent workflow skill. Client
// adapters distribute this copy; TestInstalledGuideMatchesPlugin in
// internal/clientconfig keeps the plugin payload in sync with it.
package guide

import _ "embed"

//go:embed using-agent-camp.md
var Markdown string

// Name is the skill name clients see.
const Name = "using-agent-camp"

// Bootstrap is the short workflow delivered by startup hooks and project rules.
//
//go:embed bootstrap.md
var Bootstrap string
