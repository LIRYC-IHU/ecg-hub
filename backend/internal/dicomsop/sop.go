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
