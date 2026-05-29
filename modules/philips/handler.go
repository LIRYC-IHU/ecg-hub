package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/LIRYC-IHU/ecg-hub-module-sdk"
	pb "github.com/LIRYC-IHU/ecg-hub-module-sdk/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements the ModuleService gRPC server for Philips SierraECG XML.
type Handler struct {
	pb.UnimplementedModuleServiceServer
	health *shared.HealthHelper
}

func NewHandler() *Handler {
	return &Handler{health: shared.NewHealthHelper()}
}

func (h *Handler) GetCapabilities(_ context.Context, _ *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	return &pb.GetCapabilitiesResponse{
		Name:               "philips",
		AcceptedExtensions: []string{".xml"},
		SupportedFormats: []*pb.ExportFormat{
			{Id: "original", Label: "Original (SierraECG XML)", Extension: ".xml"},
			{Id: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
			{Id: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
		},
		Version:     "1.0.0",
		Description: "Philips SierraECG 1.03 XML parser",
	}, nil
}

func (h *Handler) Validate(_ context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	if err := validate(req.Data); err != nil {
		return &pb.ValidateResponse{Valid: false, ErrorMessage: err.Error()}, nil
	}
	return &pb.ValidateResponse{Valid: true}, nil
}

func (h *Handler) Parse(_ context.Context, req *pb.ParseRequest) (*pb.ParseResponse, error) {
	meta, err := parse(req.Data)
	if err != nil {
		return nil, err
	}

	extra, _ := structpb.NewStruct(meta.Extra)

	var recordedAt *timestamppb.Timestamp
	if !meta.RecordedAt.IsZero() {
		recordedAt = timestamppb.New(meta.RecordedAt)
	}

	return &pb.ParseResponse{
		Metadata: &pb.ECGMetadata{
			PatientId:       meta.PatientID,
			RecordedAt:      recordedAt,
			VendorName:      "philips",
			SourceFormat:    meta.SourceFormat,
			DeviceModel:     meta.DeviceModel,
			LeadCount:       int32(meta.LeadCount),
			DurationSeconds: meta.DurationSeconds,
			SampleRate:      meta.SampleRate,
			Extra:           extra,
		},
	}, nil
}

func (h *Handler) UpdateFile(_ context.Context, req *pb.UpdateFileRequest) (*pb.UpdateFileResponse, error) {
	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		return &pb.UpdateFileResponse{Success: false, ErrorMessage: fmt.Sprintf("read: %v", err)}, nil
	}

	fields := patchToMap(req.Patch)
	updated, err := applyUpdates(data, fields)
	if err != nil {
		return &pb.UpdateFileResponse{Success: false, ErrorMessage: err.Error()}, nil
	}

	if err := os.WriteFile(req.FilePath, updated, 0o644); err != nil {
		return &pb.UpdateFileResponse{Success: false, ErrorMessage: fmt.Sprintf("write: %v", err)}, nil
	}
	return &pb.UpdateFileResponse{Success: true}, nil
}

func (h *Handler) RenamePatientID(_ context.Context, req *pb.RenamePatientIDRequest) (*pb.RenamePatientIDResponse, error) {
	if req.NewPatientId == "" {
		return nil, fmt.Errorf("new_patient_id is empty")
	}
	result, err := applyUpdates(req.Data, map[string]string{"patient_id": req.NewPatientId})
	if err != nil {
		return nil, err
	}
	return &pb.RenamePatientIDResponse{Data: result}, nil
}

func (h *Handler) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return h.health.Serving(ctx, req)
}

func patchToMap(patch *pb.MetadataPatch) map[string]string {
	if patch == nil {
		return nil
	}
	m := map[string]string{}
	if patch.PatientId != nil {
		m["patient_id"] = *patch.PatientId
	}
	if patch.RecordedAt != nil {
		m["recorded_at"] = patch.RecordedAt.AsTime().UTC().Format(time.RFC3339)
	}
	if patch.LastName != nil {
		m["last_name"] = *patch.LastName
	}
	if patch.FirstName != nil {
		m["first_name"] = *patch.FirstName
	}
	if patch.Sex != nil {
		m["sex"] = *patch.Sex
	}
	if patch.DeviceModel != nil {
		m["device_model"] = *patch.DeviceModel
	}
	if patch.DocumentType != nil {
		m["document_type"] = *patch.DocumentType
	}
	if patch.DocumentVersion != nil {
		m["document_version"] = *patch.DocumentVersion
	}
	return m
}
