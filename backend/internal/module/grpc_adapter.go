package module

import (
	"context"
	"fmt"
	"time"

	pb "github.com/LIRYC-IHU/ecg-hub/proto/modulepb"
	"google.golang.org/grpc"
)

// Compile-time check: GRPCModule must satisfy the Module interface.
var _ Module = (*GRPCModule)(nil)

// GRPCModule wraps a gRPC connection to a remote module service,
// presenting it as a local Module to the rest of the hub.
type GRPCModule struct {
	conn   *grpc.ClientConn
	client pb.ModuleServiceClient
	caps   *pb.GetCapabilitiesResponse
}

// NewGRPCModule connects to a remote module, fetches its capabilities,
// and returns a Module-compatible adapter.
func NewGRPCModule(conn *grpc.ClientConn) (*GRPCModule, error) {
	client := pb.NewModuleServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	caps, err := client.GetCapabilities(ctx, &pb.GetCapabilitiesRequest{})
	if err != nil {
		return nil, fmt.Errorf("grpc module: get capabilities: %w", err)
	}

	return &GRPCModule{conn: conn, client: client, caps: caps}, nil
}

func (m *GRPCModule) Name() string { return m.caps.Name }

func (m *GRPCModule) AcceptedExtensions() []string {
	return m.caps.AcceptedExtensions
}

func (m *GRPCModule) Health() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := m.client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		return err
	}
	if resp.Status != pb.HealthResponse_STATUS_SERVING {
		return fmt.Errorf("module %s: %s", m.caps.Name, resp.Message)
	}
	return nil
}

func (m *GRPCModule) SupportedFormats() []ExportFormat {
	formats := make([]ExportFormat, len(m.caps.SupportedFormats))
	for i, f := range m.caps.SupportedFormats {
		formats[i] = ExportFormat{ID: f.Id, Label: f.Label, Extension: f.Extension}
	}
	return formats
}

func (m *GRPCModule) Validate(data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := m.client.Validate(ctx, &pb.ValidateRequest{Data: data})
	if err != nil {
		return err
	}
	if !resp.Valid {
		return fmt.Errorf("%s", resp.ErrorMessage)
	}
	return nil
}

func (m *GRPCModule) Parse(ctx context.Context, data []byte) (*ECGMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := m.client.Parse(ctx, &pb.ParseRequest{Data: data})
	if err != nil {
		return nil, err
	}
	return protoToMetadata(resp.Metadata), nil
}

func (m *GRPCModule) UpdateFile(filePath string, patch MetadataPatch) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := m.client.UpdateFile(ctx, &pb.UpdateFileRequest{
		FilePath: filePath,
		Patch:    metadataPatchToProto(patch),
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.ErrorMessage)
	}
	return nil
}

func (m *GRPCModule) RenamePatientID(data []byte, newID string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := m.client.RenamePatientID(ctx, &pb.RenamePatientIDRequest{
		Data:         data,
		NewPatientId: newID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func protoToMetadata(m *pb.ECGMetadata) *ECGMetadata {
	if m == nil {
		return &ECGMetadata{}
	}
	meta := &ECGMetadata{
		PatientID:       m.PatientId,
		VendorName:      m.VendorName,
		SourceFormat:    m.SourceFormat,
		DeviceModel:     m.DeviceModel,
		LeadCount:       int(m.LeadCount),
		DurationSeconds: m.DurationSeconds,
		SampleRate:      m.SampleRate,
	}
	if m.RecordedAt != nil {
		meta.RecordedAt = m.RecordedAt.AsTime()
	}
	if m.Extra != nil {
		meta.Extra = m.Extra.AsMap()
	}
	return meta
}

func metadataPatchToProto(p MetadataPatch) *pb.MetadataPatch {
	patch := &pb.MetadataPatch{}
	if p.PatientID != nil {
		patch.PatientId = p.PatientID
	}
	if p.LastName != nil {
		patch.LastName = p.LastName
	}
	if p.FirstName != nil {
		patch.FirstName = p.FirstName
	}
	if p.Sex != nil {
		patch.Sex = p.Sex
	}
	if p.DeviceModel != nil {
		patch.DeviceModel = p.DeviceModel
	}
	return patch
}
