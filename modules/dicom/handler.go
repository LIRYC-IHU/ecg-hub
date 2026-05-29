package main

import (
	"context"
	"fmt"
	"os"

	"github.com/LIRYC-IHU/ecg-hub-module-sdk"
	pb "github.com/LIRYC-IHU/ecg-hub-module-sdk/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	pb.UnimplementedModuleServiceServer
	health *shared.HealthHelper
}

func NewHandler() *Handler {
	return &Handler{health: shared.NewHealthHelper()}
}

func (h *Handler) GetCapabilities(_ context.Context, _ *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	return &pb.GetCapabilitiesResponse{
		Name:               "dicom",
		AcceptedExtensions: []string{".dcm", ".dicom"},
		SupportedFormats: []*pb.ExportFormat{
			{Id: "original", Label: "Original (DICOM ECG)", Extension: ".dcm"},
			{Id: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		},
		Version:     "1.0.0",
		Description: "DICOM ECG parser (C-STORE / file)",
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
			PatientId:    meta.PatientID,
			RecordedAt:   recordedAt,
			VendorName:   "dicom",
			SourceFormat: "dicom",
			Extra:        extra,
		},
	}, nil
}

func (h *Handler) UpdateFile(_ context.Context, req *pb.UpdateFileRequest) (*pb.UpdateFileResponse, error) {
	if req.Patch == nil || req.Patch.PatientId == nil {
		return &pb.UpdateFileResponse{Success: true}, nil
	}

	data, err := os.ReadFile(req.FilePath)
	if err != nil {
		return &pb.UpdateFileResponse{Success: false, ErrorMessage: fmt.Sprintf("read: %v", err)}, nil
	}

	updated, err := rewritePatientID(data, *req.Patch.PatientId)
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
	result, err := rewritePatientID(req.Data, req.NewPatientId)
	if err != nil {
		return nil, err
	}
	return &pb.RenamePatientIDResponse{Data: result}, nil
}

func (h *Handler) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return h.health.Serving(ctx, req)
}
