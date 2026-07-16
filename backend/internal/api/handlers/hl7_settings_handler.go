package handlers

import "time"

// HL7SchedulerStatus exposes scheduler state to the HL7 admin handler. Shared by
// HL7AdminService (hl7_admin_service.go) and the router field type; the former
// REST HL7 settings/ping/run handlers were removed with the gRPC migration.
type HL7SchedulerStatus interface {
	LastRun() time.Time
	NextRun() time.Time
	Reload() error
	RunNow()
}
