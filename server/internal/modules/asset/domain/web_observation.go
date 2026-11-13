package domain

// SparseWebObservation contains only fields observed by an external producer.
// Missing values never clear existing HTTP evidence.
type SparseWebObservation struct {
	Title         string
	Webserver     string
	Technologies  []string
	StatusCode    *int64
	ContentLength *int64
}
