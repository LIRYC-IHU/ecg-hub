package ihe

import (
	"encoding/xml"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// Codes for the document type and status. CARD TF-2 Appendix C says the exact
// codes "are in discussion" and advises implementers to make them configurable,
// so these are the defaults rather than a decision.
//
// ponytail: constants until a site asks for different ones; move to
// module_settings (DB-driven, like the rest of the configuration) when one does.
const (
	loincSystem    = "2.16.840.1.113883.6.1"
	loincECGStudy  = "11524-6" // EKG study
	loincECGText   = "EKG study"
	statusComplete = "completed"
)

// ---------------------------------------------------------------------------
// CARD-5 response, XML flavour — CARD TF-2 Appendix C (HL7 v3 RIM).
//
// The element and attribute names are fixed by the published schema
// (AABB_MT444448), not by us: they are the contract a Display validates against.
// ---------------------------------------------------------------------------

type documentList struct {
	XMLName      xml.Name `xml:"urn:hl7-org:v3 IHEDocumentList"`
	ClassCode    string   `xml:"classCode,attr"`
	MoodCode     string   `xml:"moodCode,attr"`
	Code         codeCD   `xml:"code"`
	ActivityTime tsValue  `xml:"activityTime"`
	RecordTarget struct {
		TypeCode string `xml:"typeCode,attr"`
		Patient  struct {
			ClassCode string  `xml:"classCode,attr"`
			ID        idII    `xml:"id"`
			Person    *person `xml:"patientPatient,omitempty"`
		} `xml:"patient"`
	} `xml:"recordTarget"`
	Components []component `xml:"component"`
}

type codeCD struct {
	Code        string `xml:"code,attr"`
	CodeSystem  string `xml:"codeSystem,attr"`
	DisplayName string `xml:"displayName,attr,omitempty"`
}

type tsValue struct {
	Value string `xml:"value,attr"`
}

type idII struct {
	Extension string `xml:"extension,attr,omitempty"`
	Root      string `xml:"root,attr,omitempty"`
}

type person struct {
	ClassCode      string   `xml:"classCode,attr"`
	DeterminerCode string   `xml:"determinerCode,attr"`
	Name           *pnName  `xml:"name,omitempty"`
	Gender         *codeCD  `xml:"administrativeGenderCode,omitempty"`
	BirthTime      *tsValue `xml:"birthTime,omitempty"`
}

type pnName struct {
	Given  string `xml:"given,omitempty"`
	Family string `xml:"family,omitempty"`
}

type component struct {
	TypeCode string       `xml:"typeCode,attr"`
	Document documentInfo `xml:"documentInformation"`
}

type documentInfo struct {
	ClassCode     string   `xml:"classCode,attr"`
	MoodCode      string   `xml:"moodCode,attr"`
	ID            idII     `xml:"id"`
	Code          codeCD   `xml:"code"`
	Title         string   `xml:"title,omitempty"`
	Text          edText   `xml:"text"`
	StatusCode    codeCS   `xml:"statusCode"`
	EffectiveTime *tsValue `xml:"effectiveTime,omitempty"`
}

type codeCS struct {
	Code string `xml:"code,attr"`
}

// edText is the ED datatype carrying the hyperlink to the persistent document.
// CARD TF-2 §4.5.4.2.2 requires the list to contain a link formatted as a CARD-6
// web service request — this is that link.
type edText struct {
	MediaType string      `xml:"mediaType,attr"`
	Reference edReference `xml:"reference"`
}

type edReference struct {
	Value string `xml:"value,attr"`
}

// Entry is one ECG as the list renderers consume it — the small projection of
// models.ECG that both the XML and the XHTML flavour need.
type Entry struct {
	UID        string
	Title      string
	RecordedAt *time.Time
	Vendor     string
	// Link is the ready-made CARD-6 request for this document.
	Link string
}

// hl7TS renders an HL7 v3 TS. Nil yields "" so the caller can omit the element:
// an absent acquisition timestamp must not become a wrong one.
func hl7TS(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("20060102150405")
}

// DocumentLink builds the CARD-6 request for one document, as it is embedded in
// the list. Relative on purpose: the Display resolves it against the URL it just
// called, so the link stays correct behind a reverse proxy or a redirect, which
// an absolute URL built from a server-side guess would not.
func DocumentLink(uid string) string {
	q := url.Values{}
	q.Set("requestType", "DOCUMENT")
	q.Set("documentUID", uid)
	q.Set("preferredContentType", contentTypePDF)
	return pathDocument + "?" + q.Encode()
}

// EntriesFrom projects ECG rows into list entries.
func EntriesFrom(ecgs []models.ECG) []Entry {
	out := make([]Entry, 0, len(ecgs))
	for _, e := range ecgs {
		title := loincECGText
		if e.Vendor != "" {
			title = fmt.Sprintf("%s (%s)", loincECGText, e.Vendor)
		}
		out = append(out, Entry{
			UID:        e.ID,
			Title:      title,
			RecordedAt: e.RecordedAt,
			Vendor:     e.Vendor,
			Link:       DocumentLink(e.ID),
		})
	}
	return out
}

// RenderXML produces the SUMMARY-CARDIOLOGY-ECG response: the Appendix C
// document, preceded by the processing instruction that points a Display at the
// server-side stylesheet. §4.5.4.2.2 requires that reference so the list renders
// without any further processing on the Display side.
func RenderXML(p *models.Patient, patientID string, entries []Entry, now time.Time) ([]byte, error) {
	var doc documentList
	doc.ClassCode = "ACT"
	doc.MoodCode = "EVN"
	doc.Code = codeCD{Code: loincECGStudy, CodeSystem: loincSystem, DisplayName: loincECGText}
	doc.ActivityTime = tsValue{Value: now.Format("20060102150405")}

	doc.RecordTarget.TypeCode = "RCT"
	doc.RecordTarget.Patient.ClassCode = "PAT"
	doc.RecordTarget.Patient.ID = idII{Extension: patientID}

	if p != nil {
		per := &person{ClassCode: "PSN", DeterminerCode: "INSTANCE"}
		if p.FirstName != "" || p.LastName != "" {
			per.Name = &pnName{Given: p.FirstName, Family: p.LastName}
		}
		if g := strings.ToUpper(strings.TrimSpace(p.Gender)); g == "M" || g == "F" {
			per.Gender = &codeCD{Code: g, CodeSystem: "2.16.840.1.113883.5.1"}
		}
		if p.DateOfBirth != nil {
			per.BirthTime = &tsValue{Value: p.DateOfBirth.Format("20060102")}
		}
		doc.RecordTarget.Patient.Person = per
	}

	for _, e := range entries {
		c := component{TypeCode: "COMP"}
		c.Document = documentInfo{
			ClassCode:  "ACT",
			MoodCode:   "EVN",
			ID:         idII{Root: e.UID},
			Code:       codeCD{Code: loincECGStudy, CodeSystem: loincSystem, DisplayName: loincECGText},
			Title:      e.Title,
			Text:       edText{MediaType: contentTypePDF, Reference: edReference{Value: e.Link}},
			StatusCode: codeCS{Code: statusComplete},
		}
		if ts := hl7TS(e.RecordedAt); ts != "" {
			c.Document.EffectiveTime = &tsValue{Value: ts}
		}
		doc.Components = append(doc.Components, c)
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("ihe: marshal document list: %w", err)
	}

	var out strings.Builder
	out.WriteString(xml.Header)
	out.WriteString(`<?xml-stylesheet type="text/xsl" href="` + pathStylesheet + `"?>` + "\n")
	out.Write(body)
	out.WriteString("\n")
	return []byte(out.String()), nil
}

// ---------------------------------------------------------------------------
// CARD-5 response, XHTML flavour — the ITI-11 requestTypes.
//
// ITI-11 §3.11.4.2.2 wants XHTML Basic, so this is a self-contained document
// with no external resource of any kind: a Display renders it as-is.
// ---------------------------------------------------------------------------

var xhtmlList = template.Must(template.New("list").Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML Basic 1.0//EN"
  "http://www.w3.org/TR/xhtml-basic/xhtml-basic10.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="fr">
<head>
<title>ECG — {{.PatientLabel}}</title>
<style type="text/css">
body { font-family: sans-serif; font-size: 14px; margin: 16px; color: #111; }
h1 { font-size: 16px; margin: 0 0 2px; }
p.sub { color: #555; margin: 0 0 16px; font-size: 13px; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid #ddd; }
th { background: #f4f4f4; font-weight: bold; }
p.empty { color: #555; }
</style>
</head>
<body>
<h1>Électrocardiogrammes</h1>
<p class="sub">{{.PatientLabel}} — identifiant {{.PatientID}}</p>
{{if .Entries}}
<table>
<tr><th>Enregistré le</th><th>Document</th><th></th></tr>
{{range .Entries}}<tr>
<td>{{.Recorded}}</td>
<td>{{.Title}}</td>
<td><a href="{{.Link}}">Afficher</a></td>
</tr>
{{end}}</table>
{{else}}
<p class="empty">Aucun ECG pour ce patient.</p>
{{end}}
</body>
</html>
`))

type xhtmlRow struct {
	Recorded string
	Title    string
	Link     string
}

type xhtmlData struct {
	PatientLabel string
	PatientID    string
	Entries      []xhtmlRow
}

// RenderXHTML produces the SUMMARY / SUMMARY-CARDIOLOGY response.
func RenderXHTML(p *models.Patient, patientID string, entries []Entry) ([]byte, error) {
	data := xhtmlData{PatientLabel: "Patient", PatientID: patientID}
	if p != nil {
		if name := strings.TrimSpace(p.LastName + " " + p.FirstName); name != "" {
			data.PatientLabel = name
		}
	}
	for _, e := range entries {
		row := xhtmlRow{Recorded: "—", Title: e.Title, Link: e.Link}
		if e.RecordedAt != nil {
			row.Recorded = e.RecordedAt.Format("02/01/2006 15:04")
		}
		data.Entries = append(data.Entries, row)
	}

	var buf strings.Builder
	if err := xhtmlList.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("ihe: render xhtml list: %w", err)
	}
	return []byte(buf.String()), nil
}

// stylesheet is the default server-side XSLT the XML list points at. Appendix C
// includes an example and says explicitly that no particular one is required, so
// this renders the same table as the XHTML flavour rather than copying theirs.
const stylesheet = `<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0"
  xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  xmlns:hl7="urn:hl7-org:v3">
<xsl:output method="html" encoding="UTF-8" indent="yes"/>
<xsl:template match="/hl7:IHEDocumentList">
<html>
<head>
<title>ECG</title>
<style type="text/css">
body { font-family: sans-serif; font-size: 14px; margin: 16px; color: #111; }
h1 { font-size: 16px; margin: 0 0 2px; }
p.sub { color: #555; margin: 0 0 16px; font-size: 13px; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid #ddd; }
th { background: #f4f4f4; }
</style>
</head>
<body>
<h1>Électrocardiogrammes</h1>
<p class="sub">
  <xsl:value-of select="hl7:recordTarget/hl7:patient/hl7:patientPatient/hl7:name/hl7:family"/>
  <xsl:text> </xsl:text>
  <xsl:value-of select="hl7:recordTarget/hl7:patient/hl7:patientPatient/hl7:name/hl7:given"/>
  <xsl:text> — identifiant </xsl:text>
  <xsl:value-of select="hl7:recordTarget/hl7:patient/hl7:id/@extension"/>
</p>
<table>
<tr><th>Enregistré le</th><th>Document</th><th></th></tr>
<xsl:for-each select="hl7:component/hl7:documentInformation">
<tr>
  <td><xsl:value-of select="hl7:effectiveTime/@value"/></td>
  <td><xsl:value-of select="hl7:title"/></td>
  <td><a href="{hl7:text/hl7:reference/@value}">Afficher</a></td>
</tr>
</xsl:for-each>
</table>
</body>
</html>
</xsl:template>
</xsl:stylesheet>
`
