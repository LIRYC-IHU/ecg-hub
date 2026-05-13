package hl7

import (
	"strconv"
	"strings"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// SegmentNode represents a segment in the HL7 message tree.
type SegmentNode struct {
	Name     string      `json:"name"`
	Fields   []FieldNode `json:"fields"`
}

// FieldNode represents a field within a segment.
type FieldNode struct {
	Path       string      `json:"path"`       // e.g. "PID.5"
	Value      string      `json:"value"`
	Components []CompNode  `json:"components,omitempty"`
}

// CompNode represents a component within a field.
type CompNode struct {
	Path  string `json:"path"`  // e.g. "PID.5.1"
	Value string `json:"value"`
}

// ParseToTree parses a raw HL7 message string into a tree structure.
// Segments are split by \r, fields by |, components by ^.
func ParseToTree(raw string) []SegmentNode {
	var tree []SegmentNode
	segments := strings.Split(raw, "\r")
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		fields := strings.Split(seg, "|")
		if len(fields) == 0 {
			continue
		}
		segName := fields[0]
		node := SegmentNode{Name: segName}

		startIdx := 1
		if segName == "MSH" {
			startIdx = 2
			node.Fields = append(node.Fields, FieldNode{
				Path:  "MSH.1",
				Value: "|",
			})
			if len(fields) > 1 {
				node.Fields = append(node.Fields, FieldNode{
					Path:  "MSH.2",
					Value: fields[1],
				})
			}
		}

		for i := startIdx; i < len(fields); i++ {
			fieldIdx := i
			if segName == "MSH" {
				fieldIdx = i + 1
			}
			fieldPath := segName + "." + itoa(fieldIdx)
			components := strings.Split(fields[i], "^")

			fn := FieldNode{
				Path:  fieldPath,
				Value: fields[i],
			}

			if len(components) > 1 {
				for j, comp := range components {
					fn.Components = append(fn.Components, CompNode{
						Path:  fieldPath + "." + itoa(j+1),
						Value: comp,
					})
				}
			}

			node.Fields = append(node.Fields, fn)
		}
		tree = append(tree, node)
	}
	return tree
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// ExtractByPath extracts a value from a raw HL7 message using a path of the form
// "SEGMENT.FIELD.COMPONENT" (e.g. "PID.5.1").
//
// The path is interpreted as:
//   - SEGMENT: segment name (e.g. "PID") — find the first segment line starting with this name
//   - FIELD: 1-based field index — split segment by "|", index into the resulting slice
//   - COMPONENT (optional): 1-based component index — split field by "^", index into the resulting slice
//
// Returns an empty string if the segment, field, or component is not found.
func ExtractByPath(raw string, path string) string {
	parts := strings.SplitN(path, ".", 3)
	if len(parts) < 2 {
		return ""
	}

	segName := parts[0]
	fieldIdx, err := strconv.Atoi(parts[1])
	if err != nil || fieldIdx < 1 {
		return ""
	}

	compIdx := 0 // 0 means "return the whole field"
	if len(parts) == 3 {
		compIdx, err = strconv.Atoi(parts[2])
		if err != nil || compIdx < 1 {
			return ""
		}
	}

	// Find the segment.
	for _, seg := range strings.Split(raw, "\r") {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, segName) {
			continue
		}
		fields := strings.Split(seg, "|")
		// For MSH, field 1 is the separator itself so field indices are offset by 1.
		// For all other segments, fields[0] is the segment name, fields[1] is field 1, etc.
		if segName == "MSH" {
			// MSH.1 = "|" (separator), MSH.2 = fields[1], MSH.3 = fields[2], etc.
			if fieldIdx == 1 {
				return "|"
			}
			actualIdx := fieldIdx - 1
			if actualIdx >= len(fields) {
				return ""
			}
			return extractComponent(fields[actualIdx], compIdx)
		}

		// Non-MSH: fields[0]="PID", fields[1]=field1, etc.
		if fieldIdx >= len(fields) {
			return ""
		}
		return extractComponent(fields[fieldIdx], compIdx)
	}
	return ""
}

// extractComponent splits a field value by "^" and returns the component at the 1-based index.
// If compIdx is 0, the whole field value is returned.
func extractComponent(fieldValue string, compIdx int) string {
	if compIdx == 0 {
		return fieldValue
	}
	components := strings.Split(fieldValue, "^")
	if compIdx > len(components) {
		return ""
	}
	return components[compIdx-1]
}

// ApplyMappings applies a set of HL7 field mappings to a raw HL7 message and returns
// a populated PatientDemographics struct. Each mapping's SourcePath is used to extract
// a value from the raw message, and the TargetField determines which demographics field
// receives the extracted value.
//
// Supported TargetField values: "last_name", "first_name", "date_of_birth", "gender",
// "address", "phone".
func ApplyMappings(raw string, mappings []models.HL7Mapping) *PatientDemographics {
	d := &PatientDemographics{}
	for _, m := range mappings {
		value := ExtractByPath(raw, m.SourcePath)
		switch m.TargetField {
		case "last_name":
			d.LastName = value
		case "first_name":
			d.FirstName = value
		case "date_of_birth":
			d.DateOfBirth = value
		case "gender":
			d.Gender = value
		case "nip":
			d.NIP = value
		case "address":
			d.Address = value
		case "phone":
			d.Phone = value
		}
	}
	return d
}
