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

// Version changes automatically when either embedded prompt changes.
var Version = fmt.Sprintf("%x", sha256.Sum256([]byte(System+"\x00"+Consolidation)))
