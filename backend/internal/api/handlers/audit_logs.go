package handlers

// maxAuditPerPage caps per_page to prevent oversized queries on potentially
// large audit tables. Shared by AdminService.ListAuditLogs (admin_service.go);
// the former REST handler was removed with the gRPC migration.
const maxAuditPerPage = 200
