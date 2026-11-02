// Package dicomsop holds the DICOM SOP classes and transfer syntaxes ECG Hub
// supports. Both ends need them -- the SCP to decide what it will accept, the
// SCU to decide what it will propose -- and a second copy of either list is how
// the two quietly stop agreeing.
package dicomsop

import "slices"

// DICOM SOP Class UIDs — PS3.6 Table A-1.
const (
	// Verification SOP Class (C-ECHO).
	VerificationSOPClassUID = "1.2.840.10008.1.1"

	// 12-Lead ECG Waveform Storage.
	ECG12LeadUID = "1.2.840.10008.5.1.4.1.1.9.1.1"

	// General ECG Waveform Storage.
	GeneralECGUID = "1.2.840.10008.5.1.4.1.1.9.1.2"

	// Ambulatory ECG Waveform Storage.
	AmbulatoryECGUID = "1.2.840.10008.5.1.4.1.1.9.1.3"

	// Hemodynamic Waveform Storage.
	HemodynamicUID = "1.2.840.10008.5.1.4.1.1.9.2.1"

	// Basic Cardiac Electrophysiology Waveform Storage.
	CardiacEPUID = "1.2.840.10008.5.1.4.1.1.9.3.1"

	// Arterial Pulse Waveform Storage.
	ArterialPulseUID = "1.2.840.10008.5.1.4.1.1.9.5.1"

	// Respiratory Waveform Storage.
	RespiratoryUID = "1.2.840.10008.5.1.4.1.1.9.6.1"

	// General Audio Waveform Storage.
	GeneralAudioUID = "1.2.840.10008.5.1.4.1.1.9.4.1"
)

// DICOM Transfer Syntax UIDs — PS3.5 Table A-1.
const (
	ImplicitVRLittleEndian = "1.2.840.10008.1.2"
	ExplicitVRLittleEndian = "1.2.840.10008.1.2.1"
	ExplicitVRBigEndian    = "1.2.840.10008.1.2.2"
)

// DICOM Application Context — PS3.7 Table D.3-1.
const (
	DICOMApplicationContextUID = "1.2.840.10008.3.1.1.1"
)

// KnownECGSOPClasses lists all ECG waveform SOP classes supported by default.
var KnownECGSOPClasses = []string{
	ECG12LeadUID,
	GeneralECGUID,
	AmbulatoryECGUID,
	HemodynamicUID,
	CardiacEPUID,
	ArterialPulseUID,
	RespiratoryUID,
	GeneralAudioUID,
}

// sopClassNames are the PS3.6 names of the supported waveform classes.
var sopClassNames = map[string]string{
	ECG12LeadUID:     "12-Lead ECG",
	GeneralECGUID:    "General ECG",
	AmbulatoryECGUID: "Ambulatory ECG",
	HemodynamicUID:   "Hemodynamic Waveform",
	CardiacEPUID:     "Basic Cardiac Electrophysiology",
	ArterialPulseUID: "Arterial Pulse Waveform",
	RespiratoryUID:   "Respiratory Waveform",
	GeneralAudioUID:  "General Audio Waveform",
}

// SOPClassName returns the readable name of a waveform SOP class, or the UID
// itself when it is not one of the supported classes.
func SOPClassName(uid string) string {
	if n, ok := sopClassNames[uid]; ok {
		return n
	}
	return uid
}

// IsKnownECGSOPClass reports whether the given UID is a recognized ECG waveform SOP class.
func IsKnownECGSOPClass(uid string) bool {
	return slices.Contains(KnownECGSOPClasses, uid)
}

// SupportedTransferSyntaxes are the syntaxes ECG Hub can read and write:
// what the SCU proposes on C-STORE, and what the SCP will act on.
//
// go-netdicom falls back to dicomio.StandardTransferSyntaxes when the list is
// left empty, and that set also offers Deflated Explicit VR Little Endian —
// which go-dicom announces but cannot write: ParseTransferSyntaxUID falls
// through Deflated to Explicit VR Little Endian, so an archive selecting that
// context receives an uncompressed data set under a syntax claiming
// compression. Propose only what the encoder can actually produce.
var SupportedTransferSyntaxes = []string{
	ImplicitVRLittleEndian,
	ExplicitVRLittleEndian,
	ExplicitVRBigEndian,
}

// IsSupportedTransferSyntax reports whether ECG Hub can read and write the
// given transfer syntax. Anything else is refused rather than stored: the
// library's decoder silently resolves an unknown or compressed syntax to
// Explicit VR Little Endian, so accepting one means keeping bytes that do not
// mean what their header says.
func IsSupportedTransferSyntax(uid string) bool {
	return slices.Contains(SupportedTransferSyntaxes, uid)
}

// Implementation identity — PS3.7 D.3.3.2, PS3.10 7.1.
//
// Every association and every file ECG Hub writes has to say which
// implementation produced it; a receiver uses it to recognise a sender's
// quirks. Without these the DICOM library's own identity goes out instead, so
// ECG Hub appears on the wire as the toolkit it happens to be built on.
//
// ecg-bridge, which converts vendor formats, has its own pair
// (2.25.83057759282917960894454627989 / ECGHUB-CONV-1): it is a different
// implementation and a receiver must be able to tell the two apart.
const (
	// ImplementationClassUID identifies ECG Hub itself. Derived from a UUID
	// under the 2.25 root as PS3.5 B.2 requires, generated once and permanent:
	// changing it would make already-stored objects look like another product's.
	ImplementationClassUID = "2.25.64792898169045814434475395748197887603"

	// ImplementationVersionName is free text of at most 16 characters (PS3.7
	// D.3.3.2.3). It tracks the major version, not the release, so a receiver
	// that keys behaviour on it is not disturbed by every patch.
	ImplementationVersionName = "ECGHUB-1"
)
