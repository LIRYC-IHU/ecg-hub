package hl7

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// ListenerSettings configures the inbound ADT listener — the receiving half of
// IHE RAD-12 Patient Update, where the HIS pushes changes rather than being
// asked.
type ListenerSettings struct {
	Enabled bool
	Port    int
	// Host to bind. Empty means every interface, which is what a listener the
	// HIS has to reach generally needs.
	Host string
	// ReadTimeout bounds one message exchange. A sender that opens a connection
	// and says nothing must not hold a goroutine for the life of the process.
	ReadTimeout time.Duration

	// AllowedSenders, when non-empty, is the list of source addresses permitted
	// to send. Empty means every sender is accepted, so a deployment that has
	// not configured it is not locked out by an upgrade.
	//
	// This is real access control, and it only means anything where the source
	// address survives: under Docker bridge networking every connection appears
	// to come from the Docker gateway, so the list would either accept the
	// gateway — and therefore anything behind it — or nothing at all. It is
	// useful on a host-network deployment; elsewhere prefer AllowedFacilities,
	// with what it does and does not prove in mind.
	AllowedSenders []string
	// AllowedFacilities, when non-empty, is the list of MSH-4 sending facilities
	// permitted to send. Empty accepts every facility.
	//
	// Unlike AllowedSenders this survives NAT, because it travels in the message
	// rather than the connection. For the same reason it proves less: the value
	// is whatever the sender chose to write. It stops a misdirected feed, not an
	// attacker who can reach the port.
	AllowedFacilities []string
}

// Handler acts on a parsed inbound message and says what to answer and what it
// did.
//
// A handler that fails still gets an acknowledgement sent, with the reason as
// its text — the sender is entitled to an answer whatever happened here, and a
// silent drop is the one outcome an HL7 feed cannot diagnose.
type Handler func(msg *InboundMessage) Result

// Result is a handler's answer: what to send back, and what became of the
// message.
//
// The two are not the same thing, which is why both are recorded. An A08 for a
// patient this system does not hold and one that changed a record both answer
// AA; only the outcome tells them apart afterwards.
type Result struct {
	// AckCode is ACKAccepted, ACKError or ACKReject. Empty means accepted.
	AckCode string
	// Text goes into MSA-3 when set.
	Text string
	// Outcome is one of the models.Inbound* values. Empty means ignored.
	Outcome string
	// PatientID is the record the message concerned, when one could be read.
	PatientID string
}

// ack returns the code to send, defaulting to accepted.
func (r Result) ack() string {
	if r.AckCode == "" {
		return ACKAccepted
	}
	return r.AckCode
}

// InboundMessage is one received ADT message, parsed only as far as routing
// needs. The raw text is carried through because the field mapping configured
// for the site is what extracts values from it, not this struct.
type InboundMessage struct {
	Raw string
	// TriggerEvent is the second component of MSH-9 — "A08", "A40", and so on.
	TriggerEvent string
	// MessageType is the first component of MSH-9, normally "ADT".
	MessageType string
	// ControlID is MSH-10, echoed in the acknowledgement so the sender can match
	// it to what it sent.
	ControlID string
	// SendingFacility is MSH-4.
	SendingFacility string
	// RemoteAddr is the peer address, as seen after any NAT between us.
	RemoteAddr string
}

// InboundRecorder stores what the listener received and what became of it.
// Implemented by repository.HL7InboundRepository.
type InboundRecorder interface {
	Insert(m *models.HL7InboundMessage) error
}

// Listener accepts MLLP connections and acknowledges every message it receives.
//
// It parses only what routing needs and hands the rest to a Handler: what a
// site's messages mean is decided by the configured field mappings, not here.
type Listener struct {
	cfg      ListenerSettings
	handle   Handler
	recorder InboundRecorder
	ln       net.Listener
	done     chan struct{}
	wg       sync.WaitGroup
	stopped  chan struct{}
	once     sync.Once
	// firstPeer logs the address of the first connection, once. An operator
	// filling in AllowedSenders needs to know what this listener actually sees,
	// which is not necessarily what the sender thinks it is sending from.
	firstPeer sync.Once
}

// WithRecorder keeps a record of every message received, refusals included —
// the refusals especially, since a message turned away never reaches a handler
// and would otherwise leave no trace an operator can find. Returns l for
// chaining.
func (l *Listener) WithRecorder(r InboundRecorder) *Listener {
	l.recorder = r
	return l
}

// record stores one message outcome. Best effort: a history that cannot be
// written must never stop the sender being answered.
func (l *Listener) record(msg *InboundMessage, res Result) {
	if l.recorder == nil {
		return
	}
	outcome := res.Outcome
	if outcome == "" {
		outcome = models.InboundIgnored
	}
	err := l.recorder.Insert(&models.HL7InboundMessage{
		TriggerEvent:    msg.TriggerEvent,
		MessageType:     msg.MessageType,
		SendingFacility: msg.SendingFacility,
		ControlID:       msg.ControlID,
		RemoteAddr:      msg.RemoteAddr,
		Segments:        segmentNames(msg.Raw),
		PatientID:       res.PatientID,
		Outcome:         outcome,
		AckCode:         res.ack(),
		Reason:          res.Text,
	})
	if err != nil {
		slog.Warn("hl7 listener: could not record the message", "error", err,
			"control_id", msg.ControlID)
	}
}

// NewListener constructs a Listener. Call Start to bind.
func NewListener(cfg ListenerSettings, h Handler) *Listener {
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	return &Listener{cfg: cfg, handle: h, stopped: make(chan struct{})}
}

// Addr returns the address actually bound, which is how a test started on port
// 0 finds out where to connect.
func (l *Listener) Addr() net.Addr {
	if l.ln == nil {
		return nil
	}
	return l.ln.Addr()
}

// Start binds and serves until Stop. A disabled listener binds nothing and is
// not an error: that is how a deployment turns the feed off.
func (l *Listener) Start() error {
	if !l.cfg.Enabled {
		slog.Info("hl7 listener: disabled — not starting")
		return nil
	}
	addr := net.JoinHostPort(l.cfg.Host, fmt.Sprint(l.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("hl7 listener: listen %s: %w", addr, err)
	}
	l.ln = ln
	l.done = make(chan struct{})

	go func() {
		defer close(l.done)
		slog.Info("hl7 listener: started",
			"addr", ln.Addr().String(),
			"allowed_senders", len(l.cfg.AllowedSenders),
			"allowed_facilities", len(l.cfg.AllowedFacilities),
		)
		if len(l.cfg.AllowedSenders) > 0 {
			// Said once, loudly, because the failure is silent in both
			// directions: a list that matches nothing locks the feed out, and a
			// list that matches a shared gateway accepts everything behind it
			// while looking like access control.
			slog.Warn("hl7 listener: the sender allowlist is matched against the address this process sees, "+
				"which is the last hop when anything translates addresses in between — check the "+
				"address logged on the first connection before trusting it",
				"allowed_senders", l.cfg.AllowedSenders)
		}
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-l.stopped:
					return // Stop closed the listener; this is the normal exit.
				default:
				}
				slog.Warn("hl7 listener: accept failed", "error", err)
				return
			}
			l.wg.Add(1)
			go func() {
				defer l.wg.Done()
				l.serve(conn)
			}()
		}
	}()
	return nil
}

// Stop closes the listener and waits for the connections in flight. Safe to
// call more than once, and on a listener that never started.
func (l *Listener) Stop() {
	l.once.Do(func() {
		close(l.stopped)
		if l.ln != nil {
			_ = l.ln.Close()
			<-l.done
		}
		l.wg.Wait()
		slog.Info("hl7 listener: stopped")
	})
}

// serve handles one connection. A sender may send several messages on the same
// connection, so this loops until the peer closes or goes quiet.
func (l *Listener) serve(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	l.firstPeer.Do(func() {
		slog.Info("hl7 listener: first connection — this is the address the sender allowlist is matched against",
			"remote", remote)
	})

	if !senderAllowed(remote, l.cfg.AllowedSenders) {
		appmetrics.HL7InboundRefused.WithLabelValues("sender").Inc()
		slog.Warn("hl7 listener: refused a connection from an unlisted sender", "remote", remote)
		return
	}

	for {
		if err := conn.SetDeadline(time.Now().Add(l.cfg.ReadTimeout)); err != nil {
			return
		}
		raw, err := readMLLP(conn)
		if err != nil {
			if err != io.EOF && !strings.Contains(err.Error(), "use of closed") {
				slog.Debug("hl7 listener: connection ended", "remote", remote, "error", err)
			}
			return
		}

		msg := parseInbound(raw, remote)
		res := l.dispatch(msg)

		ack := BuildACK(msg, res.ack(), res.Text)
		if _, err := conn.Write([]byte{mllpStart}); err != nil {
			return
		}
		if _, err := conn.Write([]byte(ack)); err != nil {
			return
		}
		if _, err := conn.Write([]byte{mllpEnd, mllpCR}); err != nil {
			return
		}
	}
}

// dispatch applies the facility check and calls the handler.
func (l *Listener) dispatch(msg *InboundMessage) Result {
	if !facilityAllowed(msg.SendingFacility, l.cfg.AllowedFacilities) {
		appmetrics.HL7InboundRefused.WithLabelValues("facility").Inc()
		slog.Warn("hl7 listener: refused a message from an unlisted sending facility",
			"facility", msg.SendingFacility, "remote", msg.RemoteAddr,
			"trigger", msg.TriggerEvent, "control_id", msg.ControlID)
		res := Result{AckCode: ACKReject, Outcome: models.InboundRefused,
			Text: "sending facility not accepted by this system"}
		l.record(msg, res)
		return res
	}

	appmetrics.HL7InboundReceived.WithLabelValues(msg.TriggerEvent).Inc()
	if l.handle == nil {
		res := Result{Outcome: models.InboundIgnored}
		l.record(msg, res)
		return res
	}

	res := l.handle(msg)
	l.record(msg, res)
	return res
}

// parseInbound reads the header fields routing needs. Everything else is left
// in Raw for the site's configured mappings to extract.
func parseInbound(raw, remote string) *InboundMessage {
	msg := &InboundMessage{Raw: raw, RemoteAddr: remote}
	msg.SendingFacility = ExtractByPath(raw, "MSH.4")
	msg.ControlID = ExtractByPath(raw, "MSH.10")

	// MSH-9 is Message Type: "ADT^A08^ADT_A01". The first component says what
	// kind of message it is, the second which event triggered it — and it is the
	// second that decides what we do with it.
	msg.MessageType = ExtractByPath(raw, "MSH.9.1")
	msg.TriggerEvent = strings.ToUpper(strings.TrimSpace(ExtractByPath(raw, "MSH.9.2")))
	return msg
}

// senderAllowed reports whether a peer address passes the allowlist. An empty
// list accepts everyone.
//
// The port is stripped before comparing: a sender's source port changes on every
// connection, so a list of addresses is the only form an operator can write.
func senderAllowed(remote string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.EqualFold(a, host) {
			return true
		}
		// A CIDR lets an operator name a subnet rather than every machine on it.
		if _, subnet, err := net.ParseCIDR(a); err == nil && ip != nil && subnet.Contains(ip) {
			return true
		}
	}
	return false
}

// facilityAllowed reports whether MSH-4 passes the allowlist. An empty list
// accepts every facility.
func facilityAllowed(facility string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	facility = strings.TrimSpace(facility)
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), facility) {
			return true
		}
	}
	return false
}

// ObserveOnly is the handler for a listener that is connected but not yet
// trusted to act: it accepts and records every message without changing
// anything.
//
// It exists because what a site's ADT feed actually sends is worth seeing before
// the rules that interpret it are fixed. It answers AA, since the message was
// received and understood — a refusal would make a sender retry a message there
// is nothing wrong with.
func ObserveOnly(msg *InboundMessage) Result {
	appmetrics.HL7InboundHandled.WithLabelValues(msg.TriggerEvent, ACKAccepted).Inc()
	slog.Info("hl7 listener: message received (observing, nothing applied)",
		"trigger", msg.TriggerEvent,
		"type", msg.MessageType,
		"facility", msg.SendingFacility,
		"control_id", msg.ControlID,
		"remote", msg.RemoteAddr,
		"segments", segmentNames(msg.Raw),
		"bytes", len(msg.Raw),
	)
	return Result{Outcome: models.InboundIgnored}
}

// segmentNames lists the segments a message carries, which is what tells an
// operator whether the feed sends what the rules will need — a PID without a
// PV1, an A40 without its MRG.
func segmentNames(raw string) string {
	var names []string
	for _, seg := range strings.Split(raw, "\r") {
		seg = strings.TrimSpace(seg)
		if len(seg) < 3 {
			continue
		}
		names = append(names, seg[:3])
	}
	return strings.Join(names, ",")
}
