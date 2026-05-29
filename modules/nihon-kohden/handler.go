package main

import (
	"context"
	"fmt"

	"github.com/LIRYC-IHU/ecg-hub-module-sdk"
	pb "github.com/LIRYC-IHU/ecg-hub-module-sdk/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	pb.UnimplementedModuleServiceServer
	health *shared.HealthHelper
	ectp   *ECTPServer
}

func NewHandler(ectp *ECTPServer) *Handler {
	return &Handler{health: shared.NewHealthHelper(), ectp: ectp}
}

func (h *Handler) GetCapabilities(_ context.Context, _ *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
	return &pb.GetCapabilitiesResponse{
		Name:               "nihon-kohden",
		AcceptedExtensions: []string{".dat", ".DAT"},
		SupportedFormats: []*pb.ExportFormat{
			{Id: "original", Label: "Nihon Kohden DAT", Extension: ".DAT"},
			{Id: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		},
		Version:     "1.0.0",
		Description: "Nihon Kohden DAT parser with ECTP support",
	}, nil
}

func (h *Handler) Validate(_ context.Context, _ *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	return &pb.ValidateResponse{Valid: true}, nil
}

func (h *Handler) Parse(ctx context.Context, req *pb.ParseRequest) (*pb.ParseResponse, error) {
	meta, err := parse(ctx, req.Data)
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
			VendorName:      "nihon-kohden",
			SourceFormat:    "nihon_kohden_dat",
			DeviceModel:     meta.DeviceModel,
			LeadCount:       12,
			DurationSeconds: meta.DurationSeconds,
			SampleRate:      meta.SampleRate,
			Extra:           extra,
		},
	}, nil
}

func (h *Handler) UpdateFile(_ context.Context, _ *pb.UpdateFileRequest) (*pb.UpdateFileResponse, error) {
	return &pb.UpdateFileResponse{Success: true}, nil
}

func (h *Handler) RenamePatientID(_ context.Context, req *pb.RenamePatientIDRequest) (*pb.RenamePatientIDResponse, error) {
	if req.NewPatientId == "" {
		return nil, fmt.Errorf("new_patient_id is empty")
	}
	return &pb.RenamePatientIDResponse{Data: req.Data}, nil
}

func (h *Handler) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return h.health.Serving(ctx, req)
}
