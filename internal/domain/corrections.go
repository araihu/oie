package domain

// MessageCorrection is a verified source-identifier observation. Scope and
// Author are opaque adapter authority proofs, not display names. An empty
// TargetID registers the original source; otherwise it identifies a correction.
type MessageCorrection struct{ Scope, Author, ID, TargetID string }
