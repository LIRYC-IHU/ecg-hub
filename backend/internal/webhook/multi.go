package webhook

// EventNotifier is the minimal interface shared by the legacy static Notifier
// and the user-webhook Dispatcher (both expose Notify(event, ecgID)).
type EventNotifier interface {
	Notify(event string, ecgID string) error
}

// MultiNotifier fans a Notify call out to several notifiers. Used so HL7
// lifecycle events reach both the legacy config.yaml webhook and the
// per-user webhooks. Errors are collected best-effort: the last non-nil
// error is returned, but every target is always called.
type MultiNotifier struct {
	targets []EventNotifier
}

// NewMultiNotifier builds a MultiNotifier; nil targets are skipped.
func NewMultiNotifier(targets ...EventNotifier) *MultiNotifier {
	m := &MultiNotifier{}
	for _, t := range targets {
		if t != nil {
			m.targets = append(m.targets, t)
		}
	}
	return m
}

// Notify delivers the event to every target.
func (m *MultiNotifier) Notify(event string, ecgID string) error {
	var lastErr error
	for _, t := range m.targets {
		if err := t.Notify(event, ecgID); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
