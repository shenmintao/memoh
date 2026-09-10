package native

import (
	"sync"

	sdk "github.com/felinics/twilight/sdk"
)

// injectedMessageState delays the legacy append-only persistence callback
// until provider admission is final. A failed preflight or retry may revoke a
// message that PrepareStep appended, and the recorder has no undo operation.
type injectedMessageState struct {
	mu           sync.Mutex
	prepareCalls int
	records      []injectedMessageAdmission
}

type injectedMessageAdmission struct {
	step         int
	messageIndex int
	text         string
	admitted     bool
	applied      func()
}

func (s *injectedMessageState) nextStep() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepareCalls++
	return s.prepareCalls
}

func (s *injectedMessageState) record(step, messageIndex int, text string, callbacks ...func()) {
	if s == nil {
		return
	}
	var applied func()
	if len(callbacks) > 0 {
		applied = callbacks[0]
	}
	s.mu.Lock()
	s.records = append(s.records, injectedMessageAdmission{
		step:         step,
		messageIndex: messageIndex,
		text:         text,
		applied:      applied,
	})
	s.mu.Unlock()
}

func (s *injectedMessageState) reconcilePreparedMessages(step int, admissions []admittedPreparedMessage) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.records {
		if s.records[i].step != step {
			continue
		}
		s.records[i].admitted = preparedAdmissionsContainIndex(admissions, s.records[i].messageIndex)
	}
}

func (s *injectedMessageState) acknowledgeCommitted(step int) {
	if s == nil {
		return
	}
	var callbacks []func()
	s.mu.Lock()
	for i := range s.records {
		record := &s.records[i]
		if record.step == step && record.admitted && record.applied != nil {
			callbacks = append(callbacks, record.applied)
			record.applied = nil
		}
	}
	s.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func (s *injectedMessageState) flush(
	steps []sdk.StepResult,
	readMediaInjections []readMediaInjection,
	recorder func(headerifiedText string, insertAfter int),
) {
	if s == nil || recorder == nil {
		return
	}
	s.mu.Lock()
	records := append([]injectedMessageAdmission(nil), s.records...)
	s.mu.Unlock()

	outputCounts := make([]int, len(steps)+1)
	for i := range steps {
		outputCounts[i+1] = outputCounts[i] + len(steps[i].Messages)
	}
	for _, record := range records {
		if !record.admitted || record.step < 0 || record.step >= len(steps) {
			continue
		}
		insertAfter := outputCounts[record.step]
		for _, injection := range readMediaInjections {
			if injection.afterStep < record.step {
				insertAfter++
			}
		}
		recorder(record.text, insertAfter)
	}
}
