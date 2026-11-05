// Command camp-server is the server runtime binary: HTTP /v1 + MCP over
// the workspace store, bootstrap and token administration, backup. Run it
// under a process manager; the onboarding CLI ships as camp.
package main

import "github.com/denisqq/agent-camp/internal/server/cmd"

func main() { servercmd.Execute() }
