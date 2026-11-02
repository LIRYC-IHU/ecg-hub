package dicom

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/dicomsop"
)

// subItems walks the variable items of a hand-built A-ASSOCIATE-RQ and returns
// the User Information sub-items by type.
//
// It parses rather than searches for bytes: a sub-item found by substring would
// pass even with a length field that makes the PDU unreadable, which is the
// mistake worth catching when items are appended by hand.
func subItems(t *testing.T, pdu []byte) map[byte][]byte {
	t.Helper()
	if len(pdu) < 6 {
		t.Fatalf("PDU is %d bytes, too short to hold a header", len(pdu))
	}
	if pdu[0] != 0x01 {
		t.Fatalf("PDU type 0x%02x, want 0x01 (A-ASSOCIATE-RQ)", pdu[0])
	}
	declared := binary.BigEndian.Uint32(pdu[2:6])
	if int(declared) != len(pdu)-6 {
		t.Fatalf("PDU declares %d bytes of data, carries %d", declared, len(pdu)-6)
	}

	// Fixed part: protocol version, reserved, called AE, calling AE, reserved.
	rest := pdu[6+2+2+16+16+32:]

	out := map[byte][]byte{}
	for len(rest) >= 4 {
		itemType := rest[0]
		itemLen := int(binary.BigEndian.Uint16(rest[2:4]))
		if 4+itemLen > len(rest) {
			t.Fatalf("item 0x%02x declares %d bytes, only %d left", itemType, itemLen, len(rest)-4)
		}
		body := rest[4 : 4+itemLen]
		if itemType == 0x50 { // User Information: descend into it
			inner := body
			for len(inner) >= 4 {
				subType := inner[0]
				subLen := int(binary.BigEndian.Uint16(inner[2:4]))
				if 4+subLen > len(inner) {
					t.Fatalf("sub-item 0x%02x declares %d bytes, only %d left", subType, subLen, len(inner)-4)
				}
				out[subType] = inner[4 : 4+subLen]
				inner = inner[4+subLen:]
			}
			if len(inner) != 0 {
				t.Errorf("%d trailing bytes inside User Information", len(inner))
			}
		}
		rest = rest[4+itemLen:]
	}
	if len(rest) != 0 {
		t.Errorf("%d trailing bytes after the variable items", len(rest))
	}
	return out
}

// PS3.7 D.3.3.2 makes the Implementation Class UID mandatory in the User
// Information of an A-ASSOCIATE-RQ. This PDU is built by hand and carried none,
// so a receiver could not tell what had connected to it.
func TestAssocRQ_CarriesTheImplementationIdentity(t *testing.T) {
	var buf bytes.Buffer
	if err := dicomWriteAssocRQ(&buf, "ECG-HUB", "ARCHIVE"); err != nil {
		t.Fatalf("dicomWriteAssocRQ: %v", err)
	}
	items := subItems(t, buf.Bytes())

	if _, ok := items[0x51]; !ok {
		t.Error("the Maximum Length sub-item went missing")
	}

	classUID, ok := items[0x52]
	if !ok {
		t.Fatal("no Implementation Class UID sub-item (0x52), which PS3.7 D.3.3.2 requires")
	}
	if got := string(bytes.TrimRight(classUID, "\x00")); got != dicomsop.ImplementationClassUID {
		t.Errorf("Implementation Class UID %q, want %q", got, dicomsop.ImplementationClassUID)
	}

	versionName, ok := items[0x55]
	if !ok {
		t.Fatal("no Implementation Version Name sub-item (0x55)")
	}
	if got := string(bytes.TrimRight(versionName, "\x00 ")); got != dicomsop.ImplementationVersionName {
		t.Errorf("Implementation Version Name %q, want %q", got, dicomsop.ImplementationVersionName)
	}
}

// A UID on the wire is padded to an even length (PS3.5 9.1). An odd one leaves
// a receiver that trusts the declared length reading into the next item.
func TestAssocRQ_SubItemsAreEvenLength(t *testing.T) {
	var buf bytes.Buffer
	if err := dicomWriteAssocRQ(&buf, "ECG-HUB", "ARCHIVE"); err != nil {
		t.Fatalf("dicomWriteAssocRQ: %v", err)
	}
	for subType, body := range subItems(t, buf.Bytes()) {
		if len(body)%2 != 0 {
			t.Errorf("sub-item 0x%02x is %d bytes, an odd length", subType, len(body))
		}
	}
}

func TestWritePDUSubItem_PadsOddPayloads(t *testing.T) {
	var buf bytes.Buffer
	writePDUSubItem(&buf, 0x52, "1.2.3") // five characters
	got := buf.Bytes()

	if len(got) != 4+6 {
		t.Fatalf("wrote %d bytes, want 4 of header and 6 of padded payload", len(got))
	}
	if n := binary.BigEndian.Uint16(got[2:4]); int(n) != 6 {
		t.Errorf("declared length %d, want 6 — the padding byte must be counted", n)
	}
	if got[len(got)-1] != 0x00 {
		t.Errorf("payload padded with 0x%02x, want a NULL", got[len(got)-1])
	}
}
