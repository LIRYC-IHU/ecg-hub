package handlers

import (
	"reflect"
	"testing"
)

// A DICOM setting that survives the trip to the wire and back is a setting the
// operator can actually apply. TLS and StrictSOP existed on the connector model
// for a release without being carried here, so they could be neither saved nor
// shown, and outbound forwards stayed in clear whatever the intent.
func TestConnectorConfig_ProtoRoundTrip_KeepsDICOMSettings(t *testing.T) {
	in := ConnectorStoredConfig{
		Name:                       "pacs",
		Protocol:                   "dicom_cstore",
		Extensions:                 []string{".dcm"},
		Vendors:                    []string{"ge"},
		MaxAttempts:                5,
		Interval:                   "1m",
		WaitForHL7:                 true,
		DICOMHost:                  "pacs.example.org",
		DICOMPort:                  2762,
		CallingAE:                  "ECG-HUB",
		CalledAE:                   "ARCHIVE",
		DICOMTimeout:               "45s",
		DICOMTLS:                   true,
		DICOMTLSInsecureSkipVerify: true,
		DICOMStrictSOP:             true,
	}

	out := connectorConfigFromProto(connectorConfigToProto(in))

	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip changed the config:\n in: %+v\nout: %+v", in, out)
	}
}

// False must survive too: a true-only check would pass on a converter that
// hard-codes the field.
func TestConnectorConfig_ProtoRoundTrip_KeepsDICOMSettingsOff(t *testing.T) {
	in := ConnectorStoredConfig{
		Name:       "pacs",
		Protocol:   "dicom_cstore",
		Extensions: []string{},
		Vendors:    []string{},
		DICOMHost:  "pacs.example.org",
		DICOMPort:  104,
	}

	out := connectorConfigFromProto(connectorConfigToProto(in))

	if out.DICOMTLS || out.DICOMTLSInsecureSkipVerify || out.DICOMStrictSOP {
		t.Errorf("round trip turned settings on: tls=%v skipVerify=%v strictSOP=%v",
			out.DICOMTLS, out.DICOMTLSInsecureSkipVerify, out.DICOMStrictSOP)
	}
}
