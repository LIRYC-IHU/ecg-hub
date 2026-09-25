package hl7

import (
	"fmt"
	"strings"
	"time"
)

// Acknowledgement codes, as HL7 table 0008 defines them.
const (
	ACKAccepted = "AA" // Application Accept
	ACKError    = "AE" // Application Error — understood, could not be applied
	ACKReject   = "AR" // Application Reject — refused before being applied
)

// BuildACK renders the acknowledgement for a received message.
//
// RAD-12 requires one for every ADT message it defines, and an HL7 sender
// generally blocks or retries until it gets one — so this is built from whatever
// arrived, including a message too malformed to route. A missing control ID
// yields an empty MSA-2 rather than no acknowledgement at all: a sender that
// cannot match the answer to its message is still better off than one left
// waiting.
//
// The MSH addressing is mirrored: our sending application and facility are the
// receiving ones from the message we are answering, and vice versa.
func BuildACK(msg *InboundMessage, code, text string) string {
	if code == "" {
		code = ACKAccepted
	}
	ts := time.Now().UTC().Format("20060102150405")

	var sendingApp, sendingFac, receivingApp, receivingFac, version string
	if msg != nil {
		// Their receiver is us; our receiver is them.
		sendingApp = ExtractByPath(msg.Raw, "MSH.5")
		sendingFac = ExtractByPath(msg.Raw, "MSH.6")
		receivingApp = ExtractByPath(msg.Raw, "MSH.3")
		receivingFac = ExtractByPath(msg.Raw, "MSH.4")
		version = ExtractByPath(msg.Raw, "MSH.12")
	}
	if version == "" {
		version = "2.5"
	}

	controlID := ""
	if msg != nil {
		controlID = msg.ControlID
	}

	msh := fmt.Sprintf("MSH|^~\\&|%s|%s|%s|%s|%s||ACK|%s|P|%s",
		esc(sendingApp), esc(sendingFac),
		esc(receivingApp), esc(receivingFac),
		ts, ts, esc(version))

	msa := "MSA|" + code + "|" + esc(controlID)
	if text != "" {
		msa += "|" + esc(text)
	}
	return msh + "\r" + msa + "\r"
}

// AckCode extracts MSA-1 from an acknowledgement, for a caller checking what
// its own message was answered with.
func AckCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(ExtractByPath(raw, "MSA.1")))
}
