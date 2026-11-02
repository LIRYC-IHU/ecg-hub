package dicom

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
