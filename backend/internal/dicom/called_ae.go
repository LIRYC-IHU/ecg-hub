package dicom

import (
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// A-ASSOCIATE-RQ layout (PS3.8 9.3.2): a 6-byte PDU header, then protocol
// version (2), reserved (2), Called AE Title (16), Calling AE Title (16).
const (
	pduTypeAssociateRQ = 0x01
	assocRQPeekLen     = 6 + 2 + 2 + 16 + 16
	calledAEOffset     = 10
	callingAEOffset    = 26

	// How long a peer has to send its A-ASSOCIATE-RQ before we give up. The
	// standard's ARTIM timer plays the same role on the library side.
	assocRQReadTimeout = 30 * time.Second
)

// associateRJCalledAE is an A-ASSOCIATE-RJ (PS3.8 9.3.4): result 1
// (rejected-permanent), source 1 (DICOM UL service-user), reason 7
// (called-AE-title-not-recognized).
var associateRJCalledAE = []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x04, 0x00, 0x01, 0x01, 0x07}

// replayConn hands the bytes already read back to whoever reads next, so the
// library sees the A-ASSOCIATE-RQ from its first byte.
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// checkCalledAE reads the head of the association request and refuses it when
// the Called AE Title is not ours.
//
// It is done here, before the connection reaches go-netdicom, because the
// library never looks at the Called AE Title: it echoes whatever was asked
// for back in the A-ASSOCIATE-AC and passes neither title to any callback. So
// a device configured with the wrong title — the classic sign of a modality
// pointed at the wrong archive — was accepted and stored silently.
//
// The returned conn must be used in place of conn: the peeked bytes live in
// it. Wrapping hides the *tls.Conn from the library, so ConnectionState.TLS
// comes back empty; no callback here reads it.
//
// ok=false means the association was refused (or never arrived) and conn is
// closed.
func checkCalledAE(conn net.Conn, want string) (net.Conn, bool) {
	head := make([]byte, assocRQPeekLen)
	_ = conn.SetReadDeadline(time.Now().Add(assocRQReadTimeout))
	n, err := io.ReadFull(conn, head)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		slog.Warn("dicom: no association request read — connection closed",
			"remote_addr", remoteAddr(conn), "error", err)
		_ = conn.Close()
		return nil, false
	}
	replay := &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(head[:n]), conn)}

	// Anything but an A-ASSOCIATE-RQ, or a length too short to hold one, is the
	// library's to reject: it answers protocol errors with an A-ABORT.
	if head[0] != pduTypeAssociateRQ || binary.BigEndian.Uint32(head[2:6]) < assocRQPeekLen-6 {
		return replay, true
	}

	called := aeTitle(head[calledAEOffset : calledAEOffset+16])
	if called == aeTitle([]byte(want)) {
		return replay, true
	}

	appmetrics.DICOMSCPErrors.WithLabelValues("called_ae_mismatch").Inc()
	slog.Warn("dicom: association refused — Called AE Title is not ours",
		"called_ae", called,
		"calling_ae", aeTitle(head[callingAEOffset:callingAEOffset+16]),
		"expected", want,
		"remote_addr", remoteAddr(conn),
	)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write(associateRJCalledAE)
	// The requestor closes after reading an RJ (PS3.8 9.2). Closing first, with
	// the rest of its request still unread, makes the kernel send a reset that
	// can destroy the RJ in flight, so wait for its close instead — bounded.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.Copy(io.Discard, conn)
	_ = conn.Close()
	return nil, false
}

// aeTitle trims the space padding PS3.5 6.2 declares insignificant on an AE.
func aeTitle(b []byte) string {
	return strings.Trim(string(bytes.TrimRight(b, "\x00")), " ")
}

func remoteAddr(conn net.Conn) string {
	if conn.RemoteAddr() == nil {
		return ""
	}
	return conn.RemoteAddr().String()
}
