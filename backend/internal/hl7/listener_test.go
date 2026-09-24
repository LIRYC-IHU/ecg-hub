package hl7

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The receiving half of IHE RAD-12: the HIS pushes patient changes instead of
// being asked for them. Every message must be acknowledged — an HL7 sender
// generally blocks or retries until it is, so a silent drop stalls the feed.

const a08 = "MSH|^~\\&|MIRTH|CHU_BORDEAUX|ECG-HUB|LIRYC|20260924100000||ADT^A08^ADT_A01|MSG0001|P|2.5\r" +
	"EVN|A08|20260924100000\r" +
	"PID|||BS1172||Petit^Yannick||19651217|M\r" +
	"PV1||O\r"

func startListener(t *testing.T, cfg ListenerSettings, h Handler) *Listener {
	t.Helper()
	cfg.Enabled = true
	cfg.Host = "127.0.0.1"
	cfg.Port = 0 // any free port; Addr reports which
	l := NewListener(cfg, h)
	if err := l.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(l.Stop)
	return l
}

// send delivers one MLLP-framed message and returns the acknowledgement.
func send(t *testing.T, addr net.Addr, msg string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	frame := append([]byte{mllpStart}, append([]byte(msg), mllpEnd, mllpCR)...)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write: %v", err)
	}
	ack, err := readMLLP(conn)
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	return ack
}

func TestListener_AcknowledgesAndRoutesByTriggerEvent(t *testing.T) {
	var got *InboundMessage
	l := startListener(t, ListenerSettings{}, func(m *InboundMessage) (string, string) {
		got = m
		return ACKAccepted, ""
	})

	ack := send(t, l.Addr(), a08)

	if AckCode(ack) != ACKAccepted {
		t.Errorf("ack code = %q, want AA:\n%s", AckCode(ack), ack)
	}
	// The sender matches the answer to its message by MSA-2.
	if id := ExtractByPath(ack, "MSA.2"); id != "MSG0001" {
		t.Errorf("MSA-2 = %q, want the control ID we sent", id)
	}
	if got == nil {
		t.Fatal("the handler was never called")
	}
	// MSH-9's second component is what decides A08 from A40; the first only says
	// it is an ADT at all.
	if got.TriggerEvent != "A08" || got.MessageType != "ADT" {
		t.Errorf("routed as %q/%q, want ADT/A08", got.MessageType, got.TriggerEvent)
	}
	if got.SendingFacility != "CHU_BORDEAUX" {
		t.Errorf("SendingFacility = %q", got.SendingFacility)
	}
	if !strings.Contains(got.Raw, "Petit^Yannick") {
		t.Error("the raw message did not reach the handler — the site's mappings need it")
	}
}

func TestListener_MirrorsTheAddressingInTheAcknowledgement(t *testing.T) {
	// Their receiver is us, and ours is them; a sender that checks the envelope
	// otherwise discards the answer.
	l := startListener(t, ListenerSettings{}, nil)
	ack := send(t, l.Addr(), a08)

	if app := ExtractByPath(ack, "MSH.3"); app != "ECG-HUB" {
		t.Errorf("MSH-3 = %q, want the receiving application of the message we answered", app)
	}
	if fac := ExtractByPath(ack, "MSH.6"); fac != "CHU_BORDEAUX" {
		t.Errorf("MSH-6 = %q, want the sending facility of the message we answered", fac)
	}
}

func TestListener_AcknowledgesEvenAMessageItCannotRoute(t *testing.T) {
	// A sender that gets nothing back retries forever. An answer it can match,
	// even a refusal, is what lets the feed move on.
	l := startListener(t, ListenerSettings{}, func(*InboundMessage) (string, string) {
		return ACKError, "not something this system can apply"
	})

	ack := send(t, l.Addr(), "MSH|^~\\&|X|Y|Z|W|20260924100000||ADT^A99|MSG9|P|2.5\r")

	if AckCode(ack) != ACKError {
		t.Errorf("ack code = %q, want AE:\n%s", AckCode(ack), ack)
	}
	if !strings.Contains(ack, "not something this system can apply") {
		t.Errorf("the reason did not reach MSA-3:\n%s", ack)
	}
}

func TestListener_SeveralMessagesOnOneConnection(t *testing.T) {
	// A feed keeps its connection open and sends message after message.
	var mu sync.Mutex
	var count int
	l := startListener(t, ListenerSettings{}, func(*InboundMessage) (string, string) {
		mu.Lock()
		count++
		mu.Unlock()
		return ACKAccepted, ""
	})

	conn, err := net.DialTimeout("tcp", l.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	for i := range 3 {
		msg := strings.Replace(a08, "MSG0001", fmt.Sprintf("MSG%04d", i), 1)
		frame := append([]byte{mllpStart}, append([]byte(msg), mllpEnd, mllpCR)...)
		if _, err := conn.Write(frame); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		ack, err := readMLLP(conn)
		if err != nil {
			t.Fatalf("read ack %d: %v", i, err)
		}
		if want := fmt.Sprintf("MSG%04d", i); ExtractByPath(ack, "MSA.2") != want {
			t.Errorf("ack %d answered %q, want %q", i, ExtractByPath(ack, "MSA.2"), want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 3 {
		t.Errorf("handled %d messages, want 3", count)
	}
}

func TestListener_RefusesAnUnlistedSendingFacility(t *testing.T) {
	called := false
	l := startListener(t, ListenerSettings{AllowedFacilities: []string{"CHU_BORDEAUX"}},
		func(*InboundMessage) (string, string) { called = true; return ACKAccepted, "" })

	// Accepted facility.
	if code := AckCode(send(t, l.Addr(), a08)); code != ACKAccepted {
		t.Errorf("listed facility answered %q, want AA", code)
	}
	if !called {
		t.Error("the handler was not reached for a listed facility")
	}

	// A message from somewhere else is refused before it can change anything.
	called = false
	other := strings.Replace(a08, "CHU_BORDEAUX", "SOMEWHERE_ELSE", 1)
	if code := AckCode(send(t, l.Addr(), other)); code != ACKReject {
		t.Errorf("unlisted facility answered %q, want AR", code)
	}
	if called {
		t.Error("the handler ran for a refused facility")
	}
}

func TestListener_AnEmptyAllowlistAcceptsEveryone(t *testing.T) {
	// An upgrade must not lock out a deployment that has configured nothing.
	l := startListener(t, ListenerSettings{}, func(*InboundMessage) (string, string) {
		return ACKAccepted, ""
	})
	if code := AckCode(send(t, l.Addr(), a08)); code != ACKAccepted {
		t.Errorf("answered %q with no allowlist configured, want AA", code)
	}
}

func TestListener_DisabledBindsNothing(t *testing.T) {
	l := NewListener(ListenerSettings{Enabled: false, Port: 1}, nil)
	if err := l.Start(); err != nil {
		t.Fatalf("a disabled listener must not be an error: %v", err)
	}
	if l.Addr() != nil {
		t.Error("a disabled listener bound an address")
	}
	l.Stop() // must not panic on a listener that never started
}

func TestSenderAllowed(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		allowed []string
		want    bool
	}{
		{"empty list accepts everyone", "10.0.0.5:5000", nil, true},
		{"exact address", "10.0.0.5:5000", []string{"10.0.0.5"}, true},
		{"another address", "10.0.0.6:5000", []string{"10.0.0.5"}, false},
		// The source port changes on every connection, so only the address can
		// be listed.
		{"the port is ignored", "10.0.0.5:61234", []string{"10.0.0.5"}, true},
		{"a subnet", "10.0.0.7:5000", []string{"10.0.0.0/24"}, true},
		{"outside the subnet", "10.1.0.7:5000", []string{"10.0.0.0/24"}, false},
		{"one of several", "10.0.0.6:5000", []string{"10.0.0.5", "10.0.0.6"}, true},
		{"blank entries are skipped", "10.0.0.6:5000", []string{"", "  "}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := senderAllowed(tc.remote, tc.allowed); got != tc.want {
				t.Errorf("senderAllowed(%q, %v) = %v, want %v", tc.remote, tc.allowed, got, tc.want)
			}
		})
	}
}
