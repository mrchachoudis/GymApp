// Package berserk implements the Berserk Rank System v1.3.
//
// Provenance, because the spec arrived in three layers and the layers
// disagree in places:
//
//   - v1.0 supplies the mathematics: the LBM/bodyweight split, allometric
//     references, the substitution table, and the attribute formulas.
//   - v1.2 patches it: the fourteen-rank ladder, the calibrated Berserk gates,
//     PROVISIONAL vs VERIFIED, and the Blood economy. Its Patch 4 restores
//     v1.0's formulas verbatim, which is why v1.0 is still the arithmetic
//     source of truth.
//   - v1.3 is errata over v1.2. Six corrections, all applied here, each marked
//     at its site with the erratum number.
//
// The one decision everything else falls out of (v1.0, opening paragraph):
// absolute strength scales against lean body mass, relative strength scales
// against total bodyweight. Fat makes you heavier without making you stronger,
// so it must not raise your strength expectations, and it must cost you where
// it genuinely costs you.
//
// Two rules govern reading this code:
//
//  1. Nothing here is a threshold the coach model evaluates. Every gate,
//     band and award is decided in Go and arrives at the prompt already
//     decided (v1.2 Patch 1, §34: the math engine owns rank, the LLM
//     narrates).
//  2. Anything the specs left underdetermined is marked with a CONSTRUCTED
//     comment naming what was assumed. There are five such places and they
//     are listed in docs/DESIGN.md.
package berserk

// Version stamps every stored score. v1.0 §14.4: when constants are retuned,
// users must see "recalibrated" rather than an unexplained rank change, and
// that is only possible if the score remembers which constants produced it.
const Version = "1.3"
