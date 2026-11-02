package handlers

import (
	"bytes"
	"context"
	"slices"
	"testing"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	dicommod "github.com/LIRYC-IHU/ecg-hub/internal/module/dicom"
)

// An unidentified DICOM file, parsed by the real DICOM module, has to give the
// wrong-patient check something to compare. It used to extract no name, birth
// date or sex, so assigning it to anyone raised no mismatch at all (#133).
func TestIdentityMismatch_SeesDICOMDemographics(t *testing.T) {
	var elems []*dicomlib.Element
	for tg, v := range map[tag.Tag]string{
		tag.TransferSyntaxUID: "1.2.840.10008.1.2.1",
		tag.SOPInstanceUID:    "1.2.3.4",
		tag.PatientName:       "TEST^NONIDENT",
		tag.PatientBirthDate:  "19700101",
		tag.PatientSex:        "F",
	} {
		e, err := dicomlib.NewElement(tg, []string{v})
		if err != nil {
			t.Fatal(err)
		}
		elems = append(elems, e)
	}
	var buf bytes.Buffer
	if err := dicomlib.Write(&buf, dicomlib.Dataset{Elements: elems}); err != nil {
		t.Fatal(err)
	}

	meta, err := (&dicommod.Module{}).Parse(context.Background(), buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	got := identityMismatch(meta, &models.Patient{
		LastName: "Lacroix", FirstName: "Marie", DateOfBirth: dob(t, "1941-09-27"), Gender: "M",
	}, true)
	for _, field := range []string{"name", "birth_date", "sex"} {
		if !slices.Contains(got, field) {
			t.Errorf("mismatch on %q not reported; got %v", field, got)
		}
	}

	// And a file that does match the patient raises nothing.
	if got := identityMismatch(meta, &models.Patient{
		LastName: "Test", FirstName: "Nonident", DateOfBirth: dob(t, "1970-01-01"), Gender: "F",
	}, true); len(got) != 0 {
		t.Errorf("matching patient reported as mismatched: %v", got)
	}
}
