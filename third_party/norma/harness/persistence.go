package harness

import "github.com/Autumn-27/norma/llm"

type checkedRecorder interface{ Err() error }
type toolJournal interface {
	RecordToolIntent(llm.ContentBlock)
	RecordToolOutcome(llm.ContentBlock)
}

func (l *loop) persistenceError() error {
	if recorder, ok := l.in.Recorder.(checkedRecorder); ok {
		return recorder.Err()
	}
	return nil
}

func (l *loop) recordToolIntent(use llm.ContentBlock) error {
	if recorder, ok := l.in.Recorder.(toolJournal); ok {
		recorder.RecordToolIntent(use)
	}
	return l.persistenceError()
}
