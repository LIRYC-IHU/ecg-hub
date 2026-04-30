package dicom_test

import (
	"context"
	"log"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	legacydicom "github.com/apaladiychuk/go-dicom"
	"github.com/apaladiychuk/go-dicom/dicomtag"
	netdicom "github.com/apaladiychuk/go-netdicom"
	"github.com/apaladiychuk/go-netdicom/dimse"

	dicomconn "github.com/LIRYC-IHU/ecg-hub/internal/connector/dicom"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ─── Mock SCP ────────────────────────────────────────────────────────────────

type mockSCP struct {
	provider *netdicom.ServiceProvider
	addr     string
	host     string
	port     int

	mu           sync.Mutex
	echoCount    int
	storeCount   int
	lastSOPClass string
}

func startMockSCP(t *testing.T) *mockSCP {
	t.Helper()
	m := &mockSCP{}

	sp, err := netdicom.NewServiceProvider(netdicom.ServiceProviderParams{
		CEcho:  m.onEcho,
		CStore: m.onStore,
	}, ":0")
	if err != nil {
		t.Fatalf("mock SCP: %v", err)
	}
	m.provider = sp
	m.addr = sp.ListenAddr().String()

	host, portStr, _ := net.SplitHostPort(m.addr)
	if host == "" || host == "::" {
		host = "127.0.0.1"
	}
	m.host = host
	m.port, _ = strconv.Atoi(portStr)

	go sp.Run()
	t.Cleanup(func() {
		// go-netdicom has no explicit Stop — the GC + test process exit handles it.
	})
	return m
}

func (m *mockSCP) onEcho(_ netdicom.ConnectionState) dimse.Status {
	m.mu.Lock()
	m.echoCount++
	m.mu.Unlock()
	return dimse.Success
}

func (m *mockSCP) onStore(
	_ netdicom.ConnectionState,
	transferSyntaxUID string,
	sopClassUID string,
	sopInstanceUID string,
	data []byte,
) dimse.Status {
	m.mu.Lock()
	m.storeCount++
	m.lastSOPClass = sopClassUID
	m.mu.Unlock()
	log.Printf("mock SCP: received C-STORE, sop=%s, instance=%s, %d bytes",
		sopClassUID, sopInstanceUID, len(data))
	return dimse.Success
}

func (m *mockSCP) cfg() config.ConnectorConfig {
	return config.ConnectorConfig{
		Name:     "test-orthanc",
		Enabled:  true,
		Protocol: "dicom_cstore",
		Filters: config.ConnectorFilters{
			Extensions: []string{".dcm"},
			Vendors:    []string{"dicom"},
		},
		DICOM: config.DICOMConnectorConfig{
			Host:      m.host,
			Port:      m.port,
			CallingAE: "TEST-SCU",
			CalledAE:  "TEST-SCP",
			Timeout:   "10s",
		},
	}
}

// ─── Accepts Tests ───────────────────────────────────────────────────────────

func TestDICOMConnector_Accepts(t *testing.T) {
	scp := startMockSCP(t)
	conn, err := dicomconn.New(scp.cfg())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		ecg    *models.ECG
		want   bool
	}{
		{
			name: "matching extension and vendor",
			ecg:  &models.ECG{OriginalFilename: "test.dcm", Vendor: "dicom"},
			want: true,
		},
		{
			name: "wrong extension",
			ecg:  &models.ECG{OriginalFilename: "test.DAT", Vendor: "dicom"},
			want: false,
		},
		{
			name: "wrong vendor",
			ecg:  &models.ECG{OriginalFilename: "test.dcm", Vendor: "philips"},
			want: false,
		},
		{
			name: "case insensitive extension",
			ecg:  &models.ECG{OriginalFilename: "test.DCM", Vendor: "dicom"},
			want: true,
		},
		{
			name: "case insensitive vendor",
			ecg:  &models.ECG{OriginalFilename: "test.dcm", Vendor: "DICOM"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := conn.Accepts(tt.ecg)
			if got != tt.want {
				t.Errorf("Accepts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDICOMConnector_Accepts_EmptyFilters(t *testing.T) {
	cfg := config.ConnectorConfig{
		Name:     "test-no-filter",
		Protocol: "dicom_cstore",
		DICOM: config.DICOMConnectorConfig{
			Host:      "127.0.0.1",
			Port:      11112,
			CallingAE: "TEST",
			CalledAE:  "TEST",
			Timeout:   "5s",
		},
	}
	conn, err := dicomconn.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ecg := &models.ECG{OriginalFilename: "anything.xml", Vendor: "philips"}
	if !conn.Accepts(ecg) {
		t.Error("expected Accepts=true with empty filters")
	}
}

// ─── Health (C-ECHO) Tests ───────────────────────────────────────────────────

func TestDICOMConnector_Health_SCPUp(t *testing.T) {
	scp := startMockSCP(t)
	conn, err := dicomconn.New(scp.cfg())
	if err != nil {
		t.Fatal(err)
	}

	if err := conn.Health(); err != nil {
		t.Fatalf("Health() with SCP up: %v", err)
	}

	scp.mu.Lock()
	defer scp.mu.Unlock()
	if scp.echoCount < 1 {
		t.Error("expected at least 1 C-ECHO received by mock SCP")
	}
}

func TestDICOMConnector_Health_SCPDown(t *testing.T) {
	cfg := config.ConnectorConfig{
		Name:     "test-down",
		Protocol: "dicom_cstore",
		DICOM: config.DICOMConnectorConfig{
			Host:      "127.0.0.1",
			Port:      1, // nothing listening
			CallingAE: "TEST",
			CalledAE:  "TEST",
			Timeout:   "1s",
		},
	}
	conn, err := dicomconn.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	err = conn.Health()
	if err == nil {
		t.Fatal("expected Health() error with SCP down, got nil")
	}
}

// ─── Forward (C-STORE) Tests ─────────────────────────────────────────────────

func TestDICOMConnector_Forward_E2E(t *testing.T) {
	scp := startMockSCP(t)
	conn, err := dicomconn.New(scp.cfg())
	if err != nil {
		t.Fatal(err)
	}

	ecg := &models.ECG{ID: "ecg-100", OriginalFilename: "test.dcm", Vendor: "dicom"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := conn.Forward(ctx, ecg, "testdata/test_ecg.dcm"); err != nil {
		t.Fatalf("Forward(): %v", err)
	}

	scp.mu.Lock()
	defer scp.mu.Unlock()
	if scp.storeCount != 1 {
		t.Errorf("expected 1 C-STORE, got %d", scp.storeCount)
	}
}

func TestDICOMConnector_Forward_VerifiesSOPClass(t *testing.T) {
	scp := startMockSCP(t)
	conn, err := dicomconn.New(scp.cfg())
	if err != nil {
		t.Fatal(err)
	}

	ecg := &models.ECG{ID: "ecg-101", OriginalFilename: "test.dcm", Vendor: "dicom"}
	ctx := context.Background()
	if err := conn.Forward(ctx, ecg, "testdata/test_ecg.dcm"); err != nil {
		t.Fatalf("Forward(): %v", err)
	}

	// Read the original file to get its SOP Class UID
	ds, err := legacydicom.ReadDataSetFromFile("testdata/test_ecg.dcm", legacydicom.ReadOptions{})
	if err != nil {
		t.Fatalf("read test file: %v", err)
	}
	elem, err := ds.FindElementByTag(dicomtag.MediaStorageSOPClassUID)
	if err != nil {
		t.Fatalf("find SOP Class: %v", err)
	}
	expectedSOP, _ := elem.GetString()

	scp.mu.Lock()
	defer scp.mu.Unlock()
	if scp.lastSOPClass != expectedSOP {
		t.Errorf("SOP class mismatch: got %q, want %q", scp.lastSOPClass, expectedSOP)
	}
}

func TestDICOMConnector_Forward_SCPDown(t *testing.T) {
	cfg := config.ConnectorConfig{
		Name:     "test-fwd-down",
		Protocol: "dicom_cstore",
		DICOM: config.DICOMConnectorConfig{
			Host:      "127.0.0.1",
			Port:      1,
			CallingAE: "TEST",
			CalledAE:  "TEST",
			Timeout:   "2s",
		},
	}
	conn, err := dicomconn.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ecg := &models.ECG{ID: "ecg-102", OriginalFilename: "test.dcm", Vendor: "dicom"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = conn.Forward(ctx, ecg, "testdata/test_ecg.dcm")
	if err == nil {
		t.Fatal("expected Forward() error with SCP down, got nil")
	}
}

func TestDICOMConnector_Forward_BadFile(t *testing.T) {
	scp := startMockSCP(t)
	conn, err := dicomconn.New(scp.cfg())
	if err != nil {
		t.Fatal(err)
	}

	ecg := &models.ECG{ID: "ecg-103", OriginalFilename: "nope.dcm", Vendor: "dicom"}
	err = conn.Forward(context.Background(), ecg, "testdata/nonexistent.dcm")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// ─── New() Tests ─────────────────────────────────────────────────────────────

func TestNew_InvalidTimeout(t *testing.T) {
	cfg := config.ConnectorConfig{
		Name: "test-bad-timeout",
		DICOM: config.DICOMConnectorConfig{
			Host:    "127.0.0.1",
			Port:    4242,
			Timeout: "not-a-duration",
		},
	}
	_, err := dicomconn.New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid timeout, got nil")
	}
}

func TestNew_Defaults(t *testing.T) {
	cfg := config.ConnectorConfig{
		Name: "test-defaults",
		DICOM: config.DICOMConnectorConfig{
			Host: "127.0.0.1",
			Port: 4242,
		},
	}
	conn, err := dicomconn.New(cfg)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if conn.Name() != "test-defaults" {
		t.Errorf("Name() = %q, want %q", conn.Name(), "test-defaults")
	}
}

// ─── SOP Class Tests ─────────────────────────────────────────────────────────

func TestIsKnownECGSOPClass(t *testing.T) {
	if !dicomconn.IsKnownECGSOPClass("1.2.840.10008.5.1.4.1.1.9.1.1") {
		t.Error("12-Lead ECG should be known")
	}
	if dicomconn.IsKnownECGSOPClass("1.2.3.4.5.6.7.8.9") {
		t.Error("random UID should not be known")
	}
}
