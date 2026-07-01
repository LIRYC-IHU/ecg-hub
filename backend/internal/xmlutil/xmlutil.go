// Package xmlutil provides charset-aware XML helpers shared by the vendor modules.
//
// Go's encoding/xml decoder rejects any document that declares a non-UTF-8 encoding
// (e.g. `<?xml version="1.0" encoding="Windows-1252"?>`) unless a CharsetReader is
// configured — it fails with "encoding X declared but Decoder.CharsetReader is nil".
// Real-world vendor ECG exports are frequently Windows-1252 / ISO-8859-1 (notably GE
// MUSE RestingECG files), so every decode path must tolerate them or those files are
// silently quarantined during ingestion routing.
package xmlutil

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html/charset"
)

// NewDecoder returns an xml.Decoder that transparently transcodes non-UTF-8
// encodings declared in the XML prolog. Use it anywhere the stdlib
// xml.NewDecoder would be used for vendor XML.
func NewDecoder(r io.Reader) *xml.Decoder {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	return dec
}

// Unmarshal is xml.Unmarshal with charset support — a drop-in replacement that
// honors the document's declared encoding.
func Unmarshal(data []byte, v any) error {
	return NewDecoder(bytes.NewReader(data)).Decode(v)
}

// RootElement returns the name (namespace + local) of the first XML start element,
// honoring the declared charset and without decoding the whole document.
func RootElement(data []byte) (xml.Name, error) {
	dec := NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return xml.Name{}, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name, nil
		}
	}
}

// xmlDeclEncodingRe captures the encoding attribute value of the XML declaration
// at the very start of the document.
var xmlDeclEncodingRe = regexp.MustCompile(`(?s)^\s*<\?xml\b[^>]*?\bencoding\s*=\s*["']([^"']+)["']`)

// ToUTF8 returns data re-encoded as UTF-8, honoring the charset declared in the XML
// prolog, and rewrites the declaration's encoding attribute to "UTF-8" so that
// downstream UTF-8-only consumers (the converter binaries, stdlib xml.Unmarshal)
// accept it. Documents that are already UTF-8 (or declare no encoding) are returned
// unchanged — callers can therefore use it unconditionally without copying overhead
// for the common case.
func ToUTF8(data []byte) ([]byte, error) {
	loc := xmlDeclEncodingRe.FindSubmatchIndex(data)
	if loc == nil {
		return data, nil // no encoding declared → already UTF-8 by XML spec default
	}
	label := string(data[loc[2]:loc[3]])
	if strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "utf8") {
		return data, nil
	}

	r, err := charset.NewReaderLabel(label, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("xmlutil: unsupported charset %q: %w", label, err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("xmlutil: transcode from %q: %w", label, err)
	}
	return rewriteDeclEncoding(out), nil
}

// rewriteDeclEncoding replaces the encoding attribute value of the XML declaration
// with "UTF-8". The declaration region is pure ASCII, so the match indices are valid
// on the already-transcoded bytes.
func rewriteDeclEncoding(data []byte) []byte {
	loc := xmlDeclEncodingRe.FindSubmatchIndex(data)
	if loc == nil {
		return data
	}
	var b bytes.Buffer
	b.Grow(len(data))
	b.Write(data[:loc[2]])
	b.WriteString("UTF-8")
	b.Write(data[loc[3]:])
	return b.Bytes()
}
