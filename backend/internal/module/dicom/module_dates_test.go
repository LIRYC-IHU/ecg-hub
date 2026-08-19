package dicom_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// buildDICOMWithTags builds a minimal valid DICOM carrying exactly the given
// string tags, so a test can leave StudyDate out entirely (or present but
// empty, which is what the Philips exports in the wild actually do).
func buildDICOMWithTags(t *testing.T, tags map[tag.Tag]string) []byte {
	t.Helper()

	elems := []*dicomlib.Element{}
	add := func(tg tag.Tag, val string) {
		e, err := dicomlib.NewElement(tg, []string{val})
		if err != nil {
			t.Fatalf("NewElement %v: %v", tg, err)
		}
		elems = append(elems, e)
	}

	add(tag.TransferSyntaxUID, "1.2.840.10008.1.2.1")
	add(tag.SOPInstanceUID, "1.2.3.4.5.6.7.8.9")
	add(tag.PatientID, "P-DATE")
	for tg, val := range tags {
		add(tg, val)
	}

	ds := dicomlib.Dataset{Elements: elems}
	var buf bytes.Buffer
	if err := dicomlib.Write(&buf, ds); err != nil {
		t.Fatalf("dicom.Write: %v", err)
	}
	return buf.Bytes()
}

func TestParse_RecordedAtTagFallback(t *testing.T) {
	tests := []struct {
		name string
		tags map[tag.Tag]string
		want time.Time
	}{
		{
			name: "StudyDate and StudyTime win",
			tags: map[tag.Tag]string{
				tag.StudyDate:   "20021122",
				tag.StudyTime:   "091000",
				tag.ContentDate: "20240101",
			},
			want: time.Date(2002, 11, 22, 9, 10, 0, 0, time.UTC),
		},
		{
			name: "StudyDate present but empty falls through to ContentDate",
			tags: map[tag.Tag]string{
				tag.StudyDate:   "",
				tag.ContentDate: "20240312",
				tag.ContentTime: "143000",
			},
			want: time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC),
		},
		{
			name: "AcquisitionDateTime carries its own time",
			tags: map[tag.Tag]string{
				tag.AcquisitionDateTime: "20240312143000.000000",
			},
			want: time.Date(2024, 3, 12, 14, 30, 0, 0, time.UTC),
		},
		{
			name: "AcquisitionDate preferred over SeriesDate",
			tags: map[tag.Tag]string{
				tag.AcquisitionDate: "20240312",
				tag.SeriesDate:      "20200101",
			},
			want: time.Date(2024, 3, 12, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "SeriesDate is the last resort",
			tags: map[tag.Tag]string{
				tag.SeriesDate: "20200101",
				tag.SeriesTime: "080000",
			},
			want: time.Date(2020, 1, 1, 8, 0, 0, 0, time.UTC),
		},
		{
			// Every Philips export in the test corpus looks like this: the date
			// elements exist but hold no value. RecordedAt must stay zero so the
			// UI can say "unknown" instead of showing the ingest date.
			name: "all date tags empty yields zero time",
			tags: map[tag.Tag]string{
				tag.StudyDate:   "",
				tag.StudyTime:   "",
				tag.ContentDate: "",
				tag.ContentTime: "",
			},
			want: time.Time{},
		},
		{
			name: "no date tags at all yields zero time",
			tags: map[tag.Tag]string{},
			want: time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, err := m.Parse(context.Background(), buildDICOMWithTags(t, tt.tags))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !meta.RecordedAt.Equal(tt.want) {
				t.Errorf("RecordedAt = %v, want %v", meta.RecordedAt, tt.want)
			}
		})
	}
}
