package persistence

// BackfillCursor is the durable position for one source's historical rows.
type BackfillCursor struct {
	SourceKind string
	LastID     int64
}
