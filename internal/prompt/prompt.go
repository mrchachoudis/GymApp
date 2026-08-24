// Package prompt holds the system prompts for the two-call pipeline.
//
// They live in .txt files so they can be edited and diffed without wading
// through Go string escaping. go:embed bakes them into the binary, so the
// deployed service has no runtime file dependency.
package prompt

import _ "embed"

//go:embed parser.txt
var Parser string

//go:embed coach.txt
var Coach string
