package fda

import (
	"bytes"
	"testing"
)

// Fukuda's exporter writes a UTF-8 BOM ahead of the XML declaration. The stdlib
// decoder hands it back as character data, so a naive re-encode emits it before
// the <?xml?> ProcInst and the encoder rejects the document — which made every
// metadata edit on a Fukuda file fail with a warning and no change on disk.
func TestApplyUpdatesHandlesUTF8BOM(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="utf-8"?>
<AnnotatedECG><subject><trialSubject><subjectDemographicPerson><name>DOE^JOHN</name></subjectDemographicPerson></trialSubject></subject></AnnotatedECG>`
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(doc)...)

	out, err := applyUpdates(withBOM, map[string]string{"name": "WAXCOIN^JOHN"})
	if err != nil {
		t.Fatalf("applyUpdates on a BOM-prefixed document: %v", err)
	}
	if !bytes.HasPrefix(out, []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("the BOM was dropped — the file no longer matches what the vendor's tools expect")
	}
	if !bytes.Contains(out, []byte("WAXCOIN^JOHN")) {
		t.Errorf("the patch was not applied: %s", out)
	}
}
