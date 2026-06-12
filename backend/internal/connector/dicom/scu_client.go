package dicom

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	legacydicom "github.com/apaladiychuk/go-dicom"
	"github.com/apaladiychuk/go-dicom/dicomtag"
	netdicom "github.com/apaladiychuk/go-netdicom"

	"github.com/LIRYC-IHU/ecg-hub/internal/connector"
)

// SCUClient is a DICOM Service Class User that sends C-ECHO and C-STORE
// requests to a remote PACS.
type SCUClient struct {
	host      string
	port      int
	callingAE string
	calledAE  string
	timeout   time.Duration
	strictSOP bool
}

// NewSCUClient creates a client from connector config. Returns an error if
// the timeout duration is unparseable.
func NewSCUClient(cfg connector.DICOMEndpoint) (*SCUClient, error) {
	timeout := 30 * time.Second
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("dicom: invalid timeout %q: %w", cfg.Timeout, err)
		}
		timeout = d
	}

	callingAE := cfg.CallingAE
	if callingAE == "" {
		callingAE = "ECGHUB"
	}
	calledAE := cfg.CalledAE
	if calledAE == "" {
		calledAE = "ANY-SCP"
	}

	return &SCUClient{
		host:      cfg.Host,
		port:      cfg.Port,
		callingAE: callingAE,
		calledAE:  calledAE,
		timeout:   timeout,
		strictSOP: cfg.StrictSOP,
	}, nil
}

func (c *SCUClient) addr() string {
	return fmt.Sprintf("%s:%d", c.host, c.port)
}

// Echo sends a standards-compliant C-ECHO to the remote PACS.
// Implements the full A-ASSOCIATE + C-ECHO-RQ + A-RELEASE handshake
// using raw DICOM PDUs, bypassing go-netdicom's CEcho() which omits
// the mandatory AffectedSOPClassUID and breaks Orthanc.
func (c *SCUClient) Echo(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}

	conn, err := net.DialTimeout("tcp", c.addr(), c.timeout)
	if err != nil {
		return fmt.Errorf("dicom: echo: connect: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	if err := dicomWriteAssocRQ(conn, c.callingAE, c.calledAE); err != nil {
		return fmt.Errorf("dicom: echo: assoc-rq: %w", err)
	}

	pduType, _, err := dicomReadPDU(conn)
	if err != nil {
		return fmt.Errorf("dicom: echo: assoc response: %w", err)
	}
	if pduType == 0x03 { // A-ASSOCIATE-RJ
		return fmt.Errorf("dicom: echo: association rejected")
	}
	if pduType != 0x02 { // expect A-ASSOCIATE-AC
		return fmt.Errorf("dicom: echo: unexpected PDU type 0x%02x", pduType)
	}

	if err := dicomWriteCEchoRQ(conn); err != nil {
		return fmt.Errorf("dicom: echo: c-echo-rq: %w", err)
	}

	pduType, payload, err := dicomReadPDU(conn)
	if err != nil {
		return fmt.Errorf("dicom: echo: c-echo-rsp: %w", err)
	}
	if pduType != 0x04 { // P-DATA-TF
		return fmt.Errorf("dicom: echo: expected P-DATA-TF, got 0x%02x", pduType)
	}
	if err := dicomCheckCEchoRSP(payload); err != nil {
		return fmt.Errorf("dicom: echo: %w", err)
	}

	_ = dicomWriteReleaseRQ(conn)
	return nil
}

// --- Raw DICOM PDU helpers (C-ECHO only) ---

const verificationUID = "1.2.840.10008.1.1"
const implicitVRLEUID = "1.2.840.10008.1.2"

func padAE(ae string) [16]byte {
	var buf [16]byte
	copy(buf[:], ae)
	for i := len(ae); i < 16; i++ {
		buf[i] = ' '
	}
	return buf
}

func dicomWriteAssocRQ(w io.Writer, callingAE, calledAE string) error {
	// Build presentation context: Verification SOP Class + Implicit VR LE
	var pc bytes.Buffer
	pc.WriteByte(0x20) // Item type: Presentation Context
	pc.WriteByte(0x00) // Reserved
	pcLenPos := pc.Len()
	pc.Write([]byte{0, 0}) // length placeholder
	pc.WriteByte(0x01)     // Presentation Context ID
	pc.Write([]byte{0, 0, 0})

	// Abstract Syntax sub-item
	absSyn := []byte(verificationUID)
	pc.WriteByte(0x30) // Abstract Syntax
	pc.WriteByte(0x00)
	binary.Write(&pc, binary.BigEndian, uint16(len(absSyn)))
	pc.Write(absSyn)

	// Transfer Syntax sub-item
	tsSyn := []byte(implicitVRLEUID)
	pc.WriteByte(0x40) // Transfer Syntax
	pc.WriteByte(0x00)
	binary.Write(&pc, binary.BigEndian, uint16(len(tsSyn)))
	pc.Write(tsSyn)

	pcBytes := pc.Bytes()
	pcItemLen := uint16(len(pcBytes) - pcLenPos - 2)
	binary.BigEndian.PutUint16(pcBytes[pcLenPos:], pcItemLen)

	// User Information sub-item
	var ui bytes.Buffer
	ui.WriteByte(0x50) // User Information
	ui.WriteByte(0x00)
	var uiInner bytes.Buffer
	// Max PDU Length
	uiInner.WriteByte(0x51)
	uiInner.WriteByte(0x00)
	binary.Write(&uiInner, binary.BigEndian, uint16(4))
	binary.Write(&uiInner, binary.BigEndian, uint32(16384))
	binary.Write(&ui, binary.BigEndian, uint16(uiInner.Len()))
	ui.Write(uiInner.Bytes())

	// Assemble variable items
	var varItems bytes.Buffer
	// Application Context
	appCtx := []byte("1.2.840.10008.3.1.1.1")
	varItems.WriteByte(0x10) // Application Context
	varItems.WriteByte(0x00)
	binary.Write(&varItems, binary.BigEndian, uint16(len(appCtx)))
	varItems.Write(appCtx)
	varItems.Write(pcBytes)
	varItems.Write(ui.Bytes())

	// PDU header
	called := padAE(calledAE)
	calling := padAE(callingAE)

	var pdu bytes.Buffer
	pdu.WriteByte(0x01) // A-ASSOCIATE-RQ
	pdu.WriteByte(0x00) // Reserved
	pduDataLen := 2 + 2 + 16 + 16 + 32 + varItems.Len()
	binary.Write(&pdu, binary.BigEndian, uint32(pduDataLen))
	binary.Write(&pdu, binary.BigEndian, uint16(1)) // Protocol Version
	binary.Write(&pdu, binary.BigEndian, uint16(0)) // Reserved
	pdu.Write(called[:])
	pdu.Write(calling[:])
	pdu.Write(make([]byte, 32)) // Reserved
	pdu.Write(varItems.Bytes())

	_, err := w.Write(pdu.Bytes())
	return err
}

func dicomWriteCEchoRQ(w io.Writer) error {
	// Build DIMSE C-ECHO-RQ command dataset (Implicit VR LE)
	var cmd bytes.Buffer

	writeElem := func(group, elem uint16, val []byte) {
		binary.Write(&cmd, binary.LittleEndian, group)
		binary.Write(&cmd, binary.LittleEndian, elem)
		binary.Write(&cmd, binary.LittleEndian, uint32(len(val)))
		cmd.Write(val)
	}

	uidBytes := append([]byte(verificationUID), 0) // padded to even
	if len(uidBytes)%2 != 0 {
		uidBytes = append(uidBytes, 0)
	}
	writeElem(0x0000, 0x0002, uidBytes) // AffectedSOPClassUID

	var cmdField [2]byte
	binary.LittleEndian.PutUint16(cmdField[:], 0x0030) // C-ECHO-RQ
	writeElem(0x0000, 0x0100, cmdField[:])              // CommandField

	var msgID [2]byte
	binary.LittleEndian.PutUint16(msgID[:], 1)
	writeElem(0x0000, 0x0110, msgID[:]) // MessageID

	var dataSetType [2]byte
	binary.LittleEndian.PutUint16(dataSetType[:], 0x0101) // No data set
	writeElem(0x0000, 0x0800, dataSetType[:])              // CommandDataSetType

	// Prepend GroupLength (0000,0000) = total length of remaining elements
	var full bytes.Buffer
	groupLen := uint32(cmd.Len())
	binary.Write(&full, binary.LittleEndian, uint16(0x0000))
	binary.Write(&full, binary.LittleEndian, uint16(0x0000))
	binary.Write(&full, binary.LittleEndian, uint32(4))
	binary.Write(&full, binary.LittleEndian, groupLen)
	full.Write(cmd.Bytes())

	// Wrap in P-DATA-TF PDV
	pdvHeader := make([]byte, 6)
	binary.BigEndian.PutUint32(pdvHeader[0:], uint32(full.Len()+2)) // PDV length
	pdvHeader[4] = 0x01                                              // Presentation Context ID
	pdvHeader[5] = 0x03                                              // Last fragment + command

	var pdu bytes.Buffer
	pdu.WriteByte(0x04) // P-DATA-TF
	pdu.WriteByte(0x00)
	binary.Write(&pdu, binary.BigEndian, uint32(len(pdvHeader)+full.Len()))
	pdu.Write(pdvHeader)
	pdu.Write(full.Bytes())

	_, err := w.Write(pdu.Bytes())
	return err
}

func dicomWriteReleaseRQ(w io.Writer) error {
	pdu := []byte{0x05, 0x00, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00}
	_, err := w.Write(pdu)
	return err
}

func dicomReadPDU(r io.Reader) (byte, []byte, error) {
	var hdr [6]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, fmt.Errorf("read PDU header: %w", err)
	}
	pduType := hdr[0]
	pduLen := binary.BigEndian.Uint32(hdr[2:6])
	if pduLen > 1<<20 {
		return pduType, nil, fmt.Errorf("PDU too large: %d", pduLen)
	}
	payload := make([]byte, pduLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return pduType, nil, fmt.Errorf("read PDU body: %w", err)
	}
	return pduType, payload, nil
}

func dicomCheckCEchoRSP(pdata []byte) error {
	// P-DATA payload: 4-byte PDV length + 1 ctx ID + 1 flags + DIMSE data
	if len(pdata) < 8 {
		return fmt.Errorf("P-DATA too short")
	}
	// Walk DIMSE elements looking for Status (0000,0900)
	dimse := pdata[6:] // skip PDV header
	for len(dimse) >= 8 {
		group := binary.LittleEndian.Uint16(dimse[0:2])
		elem := binary.LittleEndian.Uint16(dimse[2:4])
		vl := binary.LittleEndian.Uint32(dimse[4:8])
		if len(dimse) < int(8+vl) {
			break
		}
		if group == 0x0000 && elem == 0x0900 && vl >= 2 {
			status := binary.LittleEndian.Uint16(dimse[8:10])
			if status == 0x0000 {
				return nil // Success
			}
			return fmt.Errorf("C-ECHO status: 0x%04x", status)
		}
		dimse = dimse[8+vl:]
	}
	return fmt.Errorf("Status element not found in C-ECHO response")
}

// Store sends a DICOM file to the remote PACS via C-STORE.
// The file is read from disk at filePath.
func (c *SCUClient) Store(ctx context.Context, filePath string) error {
	ds, err := legacydicom.ReadDataSetFromFile(filePath, legacydicom.ReadOptions{})
	if err != nil {
		return fmt.Errorf("dicom: store: read file %q: %w", filePath, err)
	}

	sopClassUID, err := c.extractSOPClassUID(ds)
	if err != nil {
		return err
	}

	if c.strictSOP && !IsKnownECGSOPClass(sopClassUID) {
		return fmt.Errorf("dicom: store: unknown SOP class %s (strict mode)", sopClassUID)
	}
	if !IsKnownECGSOPClass(sopClassUID) {
		slog.Warn("dicom: unknown SOP class, attempting best-effort forward",
			"sop_class", sopClassUID, "file", filePath)
	}

	sopClasses := c.buildSOPClasses(sopClassUID)

	su, err := netdicom.NewServiceUser(netdicom.ServiceUserParams{
		CalledAETitle:  c.calledAE,
		CallingAETitle: c.callingAE,
		SOPClasses:     sopClasses,
	})
	if err != nil {
		return fmt.Errorf("dicom: store: create SCU: %w", err)
	}

	su.Connect(c.addr())

	type result struct {
		err error
	}
	ch := make(chan result, 1)
	go func() {
		ch <- result{err: su.CStore(ds)}
	}()

	start := time.Now()
	select {
	case <-ctx.Done():
		su.Release()
		return fmt.Errorf("dicom: store: %w", ctx.Err())
	case r := <-ch:
		su.Release()
		dur := time.Since(start)
		if r.err != nil {
			slog.Warn("dicom: c-store failed",
				"sop_class", sopClassUID,
				"file", filePath,
				"duration_ms", dur.Milliseconds(),
				"error", r.err,
			)
			return fmt.Errorf("dicom: store: %w", r.err)
		}
		slog.Info("dicom: c-store success",
			"sop_class", sopClassUID,
			"file", filePath,
			"duration_ms", dur.Milliseconds(),
		)
		return nil
	}
}

func (c *SCUClient) extractSOPClassUID(ds *legacydicom.DataSet) (string, error) {
	elem, err := ds.FindElementByTag(dicomtag.MediaStorageSOPClassUID)
	if err != nil {
		elem, err = ds.FindElementByTag(dicomtag.SOPClassUID)
		if err != nil {
			return "", fmt.Errorf("dicom: store: no SOP Class UID in file: %w", err)
		}
	}
	uid, err := elem.GetString()
	if err != nil {
		return "", fmt.Errorf("dicom: store: bad SOP Class UID value: %w", err)
	}
	return uid, nil
}

// buildSOPClasses returns the list of SOP Class UIDs to propose in the association.
// Always includes Verification + the file's own SOP class + all known ECG classes.
func (c *SCUClient) buildSOPClasses(fileSOPClass string) []string {
	seen := make(map[string]bool)
	var classes []string

	add := func(uid string) {
		if !seen[uid] {
			seen[uid] = true
			classes = append(classes, uid)
		}
	}

	add(VerificationSOPClassUID)
	add(fileSOPClass)
	for _, uid := range KnownECGSOPClasses {
		add(uid)
	}
	return classes
}
