// Package prompts embeds the human-reviewable memory consolidation prompts.
// Edit the Markdown files, not Go string literals. Rebuild to load changes.
package prompts

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
)

//go:embed system.md
var System string

//go:embed consolidation.md
var Consolidation string

//go:embed repair.md
var Repair string

// Version changes automatically when any embedded prompt changes.
var Version = fmt.Sprintf("%x", sha256.Sum256([]byte(System+"\x00"+Consolidation+"\x00"+Repair)))
